// dev-init creates ignored development credentials once; it never overwrites them.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
)

func main() {
	if err := initialize(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func initialize() error {
	if err := os.MkdirAll(".secrets", 0700); err != nil {
		return err
	}
	privatePath, publicPath := ".secrets/auth-private.pem", ".secrets/auth-public.pem"
	_, privateErr := os.Stat(privatePath)
	_, publicErr := os.Stat(publicPath)
	if errors.Is(privateErr, os.ErrNotExist) && errors.Is(publicErr, os.ErrNotExist) {
		public, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		privateDER, err := x509.MarshalPKCS8PrivateKey(private)
		if err != nil {
			return err
		}
		publicDER, err := x509.MarshalPKIXPublicKey(public)
		if err != nil {
			return err
		}
		if err = create(privatePath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}), 0600); err != nil {
			return err
		}
		if err = create(publicPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}), 0644); err != nil {
			return err
		}
	} else if privateErr != nil || publicErr != nil {
		return errors.New("incomplete key pair in .secrets; restore the matching missing key before proceeding")
	}
	if _, err := os.Stat(".env"); errors.Is(err, os.ErrNotExist) {
		secret := make([]byte, 32)
		if _, err = rand.Read(secret); err != nil {
			return err
		}
		contents := fmt.Sprintf("AUTH_DATABASE_PASSWORD=%s\nGATEWAY_PORT=8080\nALLOWED_ORIGINS=http://localhost:3000\nQSLATE_UID=%d\nQSLATE_GID=%d\n", hex.EncodeToString(secret), os.Getuid(), os.Getgid())
		if err = create(".env", []byte(contents), 0600); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	fmt.Println("Development credentials ready (.env and .secrets; existing files preserved)")
	return nil
}

func create(path string, contents []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(contents)
	closeErr := file.Close()
	return errors.Join(writeErr, closeErr)
}
