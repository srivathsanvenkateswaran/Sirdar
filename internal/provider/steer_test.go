package provider

import (
	"context"
	"errors"
	"testing"
)

type plainProvider struct{}

func (plainProvider) Name() string { return "plain" }
func (plainProvider) Start(context.Context, SessionSpec) (Session, error) {
	return nil, errors.New("not started")
}
func (plainProvider) Doctor(context.Context, string) []Check { return nil }

type steerable struct {
	plainProvider
	c   Continuation
	err error
}

func (s steerable) Continuation() Continuation { return s.c }
func (s steerable) SteerRefusal() error        { return s.err }

// A provider that says nothing about steering resumes by handle: that is
// every provider whose schema retry already goes through SessionSpec.Resume.
func TestPlanSteerDefaultsToResume(t *testing.T) {
	c, err := PlanSteer(plainProvider{})
	if err != nil || c != ContinueResume {
		t.Fatalf("PlanSteer = %q, %v; want resume", c, err)
	}
}

func TestPlanSteerHonoursTheProvidersContinuation(t *testing.T) {
	c, err := PlanSteer(steerable{c: ContinuePrimed})
	if err != nil || c != ContinuePrimed {
		t.Fatalf("PlanSteer = %q, %v; want primed", c, err)
	}
}

func TestPlanSteerRefusesWithTheProvidersReason(t *testing.T) {
	want := errors.New("no continuation here")
	c, err := PlanSteer(steerable{c: ContinueNone, err: want})
	if !errors.Is(err, want) || c != ContinueNone {
		t.Fatalf("PlanSteer = %q, %v; want the provider's own error", c, err)
	}
	c, err = PlanSteer(steerable{c: ContinueNone})
	if err == nil || c != ContinueNone {
		t.Fatalf("PlanSteer with no reason = %q, %v; want a generic refusal", c, err)
	}
}
