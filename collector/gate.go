package collector

type CheckStatus string

const (
	CheckPending CheckStatus = "pending"
	CheckPassed  CheckStatus = "passed"
	CheckFailed  CheckStatus = "failed"
	CheckBlocked CheckStatus = "blocked"
)

type GateCheck struct {
	Name     string
	Blocking bool
	Status   CheckStatus
	Details  string
}

type GateReport struct {
	PhaseID string
	Checks  []GateCheck
}

func NewGateReport(phaseID string, checks ...GateCheck) *GateReport {
	return &GateReport{
		PhaseID: phaseID,
		Checks:  checks,
	}
}

func (r *GateReport) CanCommit() bool {
	if r == nil {
		return false
	}
	for _, check := range r.Checks {
		if check.Blocking && check.Status != CheckPassed {
			return false
		}
	}
	return true
}

func (r *GateReport) CanAdvance() bool {
	return r.CanCommit()
}
