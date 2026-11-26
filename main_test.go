package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/inkly/casaos-wake-on-lan/route"
)

func TestRegisterRetriesUntilTheGatewayAnswers(t *testing.T) {
	tries, failures := 0, 0
	var waits []time.Duration
	err := register(context.Background(),
		func() error {
			tries++
			if tries < 6 {
				return errors.New("the gateway is not there yet")
			}
			return nil
		},
		func(_ context.Context, d time.Duration) bool { waits = append(waits, d); return true },
		func(error, int) { failures++ },
	)
	if err != nil || tries != 6 || failures != 5 {
		t.Fatalf("err %v, %d tries, %d failures", err, tries, failures)
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 10 * time.Second}
	for i := range want {
		if waits[i] != want[i] {
			t.Fatalf("waits %v, want %v", waits, want)
		}
	}
}

func TestRegisterStopsWithTheService(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	tries := 0
	err := register(ctx,
		func() error { tries++; return errors.New("no gateway") },
		func(context.Context, time.Duration) bool { cancel(); return false },
		func(error, int) {},
	)
	if err == nil || tries != 1 {
		t.Fatalf("err %v after %d tries, want an error after 1", err, tries)
	}
}

// The doc describes every route the router answers, under the route the module registers.
func TestTheDocDescribesTheRoutes(t *testing.T) {
	for _, want := range []string{"url: " + route.APIPath, "  /devices:", "  /wake:", "  /status:", "    put:", "    post:"} {
		if !strings.Contains(_docYAML, want) {
			t.Errorf("openapi.yaml has no %q", want)
		}
	}
}
