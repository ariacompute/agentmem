package router

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
)

func TestSystemOneReportsARequestBuildFailure(t *testing.T) {
	previous := newRequest
	newRequest = func(context.Context, string, string, io.Reader) (*http.Request, error) {
		return nil, errors.New("builder refused")
	}
	t.Cleanup(func() { newRequest = previous })

	decisioner := NewSystemOneDecisioner(SystemOneConfig{
		Endpoint:     "https://example.test",
		AllowedHosts: []string{"example.test"},
	})
	if _, err := decisioner.Decide(context.Background(), DecisionTask{Kind: "read", Text: "revenue", Eligible: []string{"memory"}}); err == nil {
		t.Fatal("a request build failure must surface")
	}
}
