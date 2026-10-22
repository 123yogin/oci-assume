package setup

// execPolicyStub is a placeholder until the real exec-policy step lands.
type execPolicyStub struct{}

// NewExecPolicyStep returns the Windows execution-policy step.
func NewExecPolicyStep() Step { return execPolicyStub{} }

func (execPolicyStub) ID() string                 { return "exec-policy" }
func (execPolicyStub) Describe(*Env) string       { return "exec-policy (not yet available)" }
func (execPolicyStub) Done(*Env) (bool, error)    { return true, nil }
func (execPolicyStub) Apply(*Env, *Receipt) error { return nil }
func (execPolicyStub) Undo(*Env, *Receipt) error  { return nil }
func (execPolicyStub) NeedsTTY() bool             { return false }
