package setup

// ociCLIStub is a placeholder until the real oci-cli step lands.
type ociCLIStub struct{}

// NewOCICLIStep returns the OCI CLI install step.
func NewOCICLIStep() Step { return ociCLIStub{} }

func (ociCLIStub) ID() string                 { return "oci-cli" }
func (ociCLIStub) Describe(*Env) string       { return "oci-cli (not yet available)" }
func (ociCLIStub) Done(*Env) (bool, error)    { return true, nil }
func (ociCLIStub) Apply(*Env, *Receipt) error { return nil }
func (ociCLIStub) Undo(*Env, *Receipt) error  { return nil }
func (ociCLIStub) NeedsTTY() bool             { return true }
