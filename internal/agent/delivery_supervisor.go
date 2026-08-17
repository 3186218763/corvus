package agent

import (
	"corvus/internal/capability"
	"corvus/internal/evidence"
)

// deliverySupervisor owns state whose lifetime is the delivery scope or one
// user turn. The Agent keeps the execution machinery and delegates these
// transitions here so a new run cannot accidentally inherit a stale readiness
// expectation by forgetting one field in beginRunTurn.
//
// This is intentionally an internal seam for now: the supervisor is deep in
// the delivery contract (scope/checkpoint/readiness handoff) while its
// interface remains a few lifecycle operations. Tool execution and evidence
// recording still stay in Agent until their ownership can move without a
// pass-through wrapper.
type deliverySupervisor struct {
	criteriaEstablished bool
	taskExpected        bool
	mutationExpected    bool
	scopeID             string
	scopeActive         bool
	checkpoint          evidence.DeliveryCheckpoint

	preserveEvidenceOnce  bool
	recoveryPending       bool
	pendingReviewWarnings []string

	capabilityLedger          *capability.Ledger
	capabilityAudit           *capability.Audit
	capabilityPreferReminded  bool
	capabilityRequireMissSeen bool
	capabilityPreferMissSeen  bool
}

func newDeliverySupervisor(ledger *capability.Ledger, audit *capability.Audit) *deliverySupervisor {
	return &deliverySupervisor{capabilityLedger: ledger, capabilityAudit: audit}
}

func (a *Agent) deliveryState() *deliverySupervisor {
	if a.delivery == nil {
		a.delivery = newDeliverySupervisor(nil, nil)
	}
	return a.delivery
}

func (d *deliverySupervisor) beginRun(preserve, scoped bool, scopeID string) {
	if d == nil {
		return
	}
	if !preserve {
		d.recoveryPending = false
	}
	if scoped {
		d.scopeID = scopeID
	} else if !preserve {
		d.scopeID = ""
	}
	d.scopeActive = scoped
	if scoped && d.checkpoint.ScopeID != scopeID {
		d.checkpoint = evidence.DeliveryCheckpoint{ScopeID: scopeID}
	}
	// These are recomputed from the current user input and current evidence by
	// beginRunTurn. Clearing them here makes that ownership explicit.
	d.criteriaEstablished = false
	d.taskExpected = false
	d.mutationExpected = false
}

func (d *deliverySupervisor) setExpectations(criteria, task, mutation bool) {
	if d == nil {
		return
	}
	d.criteriaEstablished = criteria
	d.taskExpected = task
	d.mutationExpected = mutation
}

func (d *deliverySupervisor) consumeRecovery() bool {
	if d == nil || !d.recoveryPending {
		return false
	}
	d.preserveEvidenceOnce = true
	d.recoveryPending = false
	return true
}

func (d *deliverySupervisor) markRecoveryPending() {
	if d != nil {
		d.recoveryPending = true
	}
}

func (d *deliverySupervisor) checkpointFor(scopeID string) evidence.DeliveryCheckpoint {
	if d == nil {
		return evidence.DeliveryCheckpoint{}
	}
	cp := d.checkpoint
	if cp.ScopeID != scopeID {
		cp = evidence.DeliveryCheckpoint{ScopeID: scopeID}
	}
	return cp
}
