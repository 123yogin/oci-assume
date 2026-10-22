package setup

// hookStub is a placeholder until the real shell-hook step lands.
type hookStub struct{}

// NewHookStep returns the shell hook step.
func NewHookStep() Step { return hookStub{} }

func (hookStub) ID() string                 { return "shell-hook" }
func (hookStub) Describe(*Env) string       { return "shell-hook (not yet available)" }
func (hookStub) Done(*Env) (bool, error)    { return true, nil }
func (hookStub) Apply(*Env, *Receipt) error { return nil }
func (hookStub) Undo(*Env, *Receipt) error  { return nil }
func (hookStub) NeedsTTY() bool             { return false }
