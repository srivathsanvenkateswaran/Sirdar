package provider

import "fmt"

// Continuation says how a provider carries a finished run forward when a
// person types a follow-up instruction on it (`sirdar steer`).
//
// A run's transcript is only honest about who answered the follow-up if
// the record says which of these happened: the agent that wrote the note
// picking up its own context, or a fresh agent handed that note and told
// to go on from it. The run layer writes the value into the run's state
// and event log, so it is a string a person can read rather than an enum
// only code can.
type Continuation string

const (
	// ContinueResume continues the very session the run recorded a
	// handle for: Claude's --resume, Codex's thread/resume, the openai
	// loop's transcript. The agent keeps every tool result it has seen.
	ContinueResume Continuation = "resume"

	// ContinuePrimed opens a new session and hands it the run's original
	// prompt, its previous answer and the instruction. The agent starts
	// with the note rather than with the memory of having written it.
	ContinuePrimed Continuation = "primed"

	// ContinueNone is a provider that has no way to continue at all, and
	// whose SteerRefusal says why.
	ContinueNone Continuation = ""
)

// Steerable is the optional half of the Provider contract for a provider
// that continues a finished run some way other than resuming its handle,
// or that cannot continue one at all.
//
// It is asked before a steer touches the run's state, the way FixSupport
// is asked before `sirdar fix` cuts a branch, so a refusal leaves the run
// exactly as it was. A provider that does not implement it is taken to
// resume by handle, which is what the runner's schema retry already relies
// on for every provider that records one.
type Steerable interface {
	// Continuation is how this provider continues a run, or ContinueNone.
	Continuation() Continuation
	// SteerRefusal is why not, in the words the operator should read. It
	// is only consulted when Continuation is ContinueNone.
	SteerRefusal() error
}

// PlanSteer says how a steer on p continues the run, or why it cannot.
func PlanSteer(p Provider) (Continuation, error) {
	st, ok := p.(Steerable)
	if !ok {
		return ContinueResume, nil
	}
	if c := st.Continuation(); c != ContinueNone {
		return c, nil
	}
	if err := st.SteerRefusal(); err != nil {
		return ContinueNone, err
	}
	return ContinueNone, fmt.Errorf("provider %s: `sirdar steer` is not supported", p.Name())
}
