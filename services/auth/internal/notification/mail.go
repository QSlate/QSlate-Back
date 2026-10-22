package notification

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/mail"
	"net/smtp"
	"net/url"
	"strings"
	"time"
)

type Cipher interface {
	Encrypt([]byte) ([]byte, error)
	Decrypt([]byte) ([]byte, error)
}
type Queue interface {
	ClaimMail(context.Context) (int64, []byte, error)
	CompleteMail(context.Context, int64) error
}

var ErrEmpty = errors.New("empty mail queue")

type Config struct{ Address, From, Username, Password, TLSMode, AppURL string }
type Message struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}
type Mailer struct {
	cfg    Config
	cipher Cipher
	queue  Queue
	logger *slog.Logger
}

func New(cfg Config, cipher Cipher, queue Queue, logger *slog.Logger) (*Mailer, error) {
	if _, _, err := net.SplitHostPort(cfg.Address); err != nil {
		return nil, errors.New("SMTP_ADDR must be host:port")
	}
	if address, err := mail.ParseAddress(cfg.From); err != nil || address.Address != cfg.From {
		return nil, errors.New("invalid SMTP_FROM")
	}
	if cfg.TLSMode != "starttls" && cfg.TLSMode != "tls" && cfg.TLSMode != "plain" {
		return nil, errors.New("SMTP_TLS_MODE must be starttls, tls or plain")
	}
	if (cfg.Username == "") != (cfg.Password == "") || (cfg.TLSMode == "plain" && cfg.Password != "") {
		return nil, errors.New("SMTP credentials require TLS and both username/password")
	}
	base, err := url.Parse(cfg.AppURL)
	if err != nil || base.Host == "" || (base.Scheme != "https" && base.Scheme != "http") || base.RawQuery != "" || base.Fragment != "" || base.User != nil {
		return nil, errors.New("invalid PUBLIC_APP_URL")
	}
	return &Mailer{cfg: cfg, cipher: cipher, queue: queue, logger: logger}, nil
}

func (m *Mailer) Seal(email, kind, token string) ([]byte, error) {
	path, subject := "/verify-email", "Verify your QSlate email"
	if kind == "reset_password" {
		path, subject = "/reset-password", "Reset your QSlate password"
	} else if kind != "verify_email" {
		return nil, errors.New("unknown mail action")
	}
	link := strings.TrimRight(m.cfg.AppURL, "/") + path + "?" + url.Values{"token": {token}}.Encode()
	data, err := json.Marshal(Message{To: email, Subject: subject, Body: "Follow this one-time link:\r\n" + link + "\r\nIf you did not request this, ignore this message.\r\n"})
	if err != nil {
		return nil, err
	}
	return m.cipher.Encrypt(data)
}

func (m *Mailer) Run(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		id, payload, err := m.queue.ClaimMail(ctx)
		if err == nil {
			plain, decodeErr := m.cipher.Decrypt(payload)
			var message Message
			if decodeErr == nil {
				decodeErr = json.Unmarshal(plain, &message)
			}
			if decodeErr == nil {
				decodeErr = m.send(ctx, message)
			}
			if decodeErr != nil {
				m.logger.Warn("email delivery deferred", "mail_id", id)
			} else if err = m.queue.CompleteMail(ctx, id); err != nil {
				m.logger.Warn("email acknowledgement failed", "mail_id", id)
			}
			if decodeErr == nil && err == nil {
				continue
			}
		} else if !errors.Is(err, ErrEmpty) && ctx.Err() == nil {
			m.logger.Warn("mail outbox unavailable")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (m *Mailer) send(ctx context.Context, message Message) error {
	if address, err := mail.ParseAddress(message.To); err != nil || address.Address != message.To {
		return errors.New("invalid recipient")
	}
	host, _, _ := net.SplitHostPort(m.cfg.Address)
	conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", m.cfg.Address)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err = conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}
	tlsConfig := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	if m.cfg.TLSMode == "tls" {
		secure := tls.Client(conn, tlsConfig)
		if err = secure.HandshakeContext(ctx); err != nil {
			return err
		}
		conn = secure
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer client.Close()
	if m.cfg.TLSMode == "starttls" {
		if err = client.StartTLS(tlsConfig); err != nil {
			return err
		}
	}
	if m.cfg.Username != "" {
		if err = client.Auth(smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, host)); err != nil {
			return err
		}
	}
	if err = client.Mail(m.cfg.From); err != nil {
		return err
	}
	if err = client.Rcpt(message.To); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(writer, "From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s", m.cfg.From, message.To, message.Subject, message.Body)
	if err != nil {
		_ = writer.Close()
		return err
	}
	if err = writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}
