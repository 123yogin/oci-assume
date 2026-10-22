package setup

// loginStub is a placeholder until the real first-login step lands.
type loginStub struct{}

// NewLoginStep returns the first login step.
func NewLoginStep() Step { return loginStub{} }

func (loginStub) ID() string                 { return "first-login" }
func (loginStub) Describe(*Env) string       { return "first-login (not yet available)" }
func (loginStub) Done(*Env) (bool, error)    { return true, nil }
func (loginStub) Apply(*Env, *Receipt) error { return nil }
func (loginStub) Undo(*Env, *Receipt) error  { return nil }
func (loginStub) NeedsTTY() bool             { return true }
