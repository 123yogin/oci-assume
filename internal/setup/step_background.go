package setup

// backgroundStub is a placeholder until the real background step lands.
type backgroundStub struct{}

// NewBackgroundStep returns the background refresh step.
func NewBackgroundStep() Step { return backgroundStub{} }

func (backgroundStub) ID() string                 { return "background" }
func (backgroundStub) Describe(*Env) string       { return "background (not yet available)" }
func (backgroundStub) Done(*Env) (bool, error)    { return true, nil }
func (backgroundStub) Apply(*Env, *Receipt) error { return nil }
func (backgroundStub) Undo(*Env, *Receipt) error  { return nil }
func (backgroundStub) NeedsTTY() bool             { return false }
