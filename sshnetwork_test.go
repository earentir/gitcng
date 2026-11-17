package main

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestRunWithNetworkBudgetReturnsOnTime(t *testing.T) {
	fetchTimeoutSec = 1
	err := runWithNetworkBudget(func() error {
		time.Sleep(20 * time.Millisecond)
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestRunWithNetworkBudgetTimesOut(t *testing.T) {
	fetchTimeoutSec = 1
	err := runWithNetworkBudget(func() error {
		time.Sleep(5 * time.Second)
		return nil
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
}

func TestClassifyFetchConnectionTimedOut(t *testing.T) {
	fetchTimeoutSec = 10
	raw := fmt.Errorf("dial tcp 66.96.140.174:22: connect: connection timed out")
	got := userError("fetch", raw)
	want := "fetch: cannot reach remote (connection timed out after 10s)"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}