package setup

import (
	"errors"
	"io/fs"
	"os"
	"time"

	"github.com/123yogin/oci-assume/internal/refresher"
)

// healEvery rate-limits status checks and reinstall attempts, including failed ones.
const healEvery = 10 * time.Minute

// SelfHeal silently reinstalls a consented background entry that is missing or points at a deleted binary.
// It stamps r.LastCheck on every check and r.LastHeal on every attempt; the caller saves the receipt and logs errors.
func SelfHeal(e *Env, r *Receipt, b refresher.Backend, bin string) (healed bool, err error) {
	if r.Background == nil || !r.Background.Consented {
		return false, nil
	}
	now := e.Now()
	if recent(now, r.LastCheck) || recent(now, r.LastHeal) {
		return false, nil
	}
	r.LastCheck = now
	if !needsHeal(b) {
		return false, nil
	}
	r.LastHeal = now
	if err := b.Install(bin); err != nil {
		return false, err
	}
	return true, nil
}

// needsHeal: not installed, status unreadable, or the definition's binary is gone.
func needsHeal(b refresher.Backend) bool {
	st, err := b.Status()
	if err != nil && !errors.Is(err, refresher.ErrLastRun) {
		return true
	}
	if !st.Installed {
		return true
	}
	br, ok := b.(refresher.BinReporter)
	if !ok {
		return false
	}
	p, err := br.InstalledBin()
	if err != nil || p == "" {
		return false
	}
	_, err = os.Stat(p)
	return errors.Is(err, fs.ErrNotExist)
}

// recent: t is set, not in the future, and less than healEvery ago.
func recent(now, t time.Time) bool {
	return !t.IsZero() && !now.Before(t) && now.Sub(t) < healEvery
}
