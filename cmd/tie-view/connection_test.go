package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/uidbz/tie/client"
)

func TestTagLoadErrorEmptyCollection(t *testing.T) {
	if got := tagLoadError(client.ErrNotFound); got != "No tags yet in this collection." {
		t.Fatalf("tagLoadError(ErrNotFound) = %q", got)
	}
}

func TestTagLoadErrorKeepsCause(t *testing.T) {
	err := errors.New("dial tcp: lookup storage: no such host")
	got := tagLoadError(err)
	if !strings.Contains(got, err.Error()) {
		t.Fatalf("tagLoadError = %q, want the underlying error", got)
	}
}
