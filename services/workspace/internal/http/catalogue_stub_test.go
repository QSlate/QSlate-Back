package http

import "context"

// Unit/repository tests isolate catalogue transport, covered separately and in E2E.
type testCatalogue struct{}

func (testCatalogue) Validate(context.Context, int64, string, string, bool) error { return nil }
