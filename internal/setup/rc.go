package setup

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/123yogin/oci-assume/internal/shell"
)

const (
	markBegin = "# >>> oci-assume >>>"
	markEnd   = "# <<< oci-assume <<<"
	bakSuffix = ".oci-assume.bak"
	bom       = "\uFEFF"
	execWait  = 10 * time.Second
	pwshWait  = 30 * time.Second
)

// pwshPathQuery forces UTF-8 output so non-ASCII profile paths survive.
const pwshPathQuery = "[Console]::OutputEncoding=[Text.Encoding]::UTF8; $PROFILE.CurrentUserAllHosts" // #nosec G101 -- a PowerShell expression, not a credential

// errNoShell marks a skipped hook step (no shell or no PowerShell); the hook step maps it to ErrSkipped.
var errNoShell = errors.New("shell hook skipped")

// ErrUnknownShell means no flag, parent process or $SHELL named a supported shell.
var ErrUnknownShell = fmt.Errorf("%w: could not detect your shell", errNoShell)

var errNested = errors.New("nested oci-assume begin marker; fix the file by hand")

// windowsParent names the parent and grandparent processes on Windows; a var so tests can fake it.
var windowsParent = parentProcessName

// Target is one file the hook step writes: Kind "block" (marked region) or "dropin" (whole file).
type Target struct {
	Path, Kind, Content string
}

// hookLine is the guarded line for each shell; static apart from the shell name.
func hookLine(sh string) string {
	switch sh {
	case "zsh", "bash":
		return `command -v oci-assume >/dev/null && eval "$(oci-assume init ` + sh + `)"`
	case "fish":
		return "if command -q oci-assume; oci-assume init fish | source; end"
	case "pwsh":
		return "if (Get-Command oci-assume -ErrorAction SilentlyContinue) { oci-assume init pwsh | Out-String | Invoke-Expression }"
	}
	return ""
}

const fishHeader = "# Added by oci-assume setup; removed by oci-assume uninstall.\n"

// macOS login shells read only the first of .bash_profile, .bash_login, .profile.
const (
	bashProfileLine = `[ -f ~/.bashrc ] && . ~/.bashrc`
	profileLine     = `[ -f ~/.profile ] && . ~/.profile`
)

var (
	sourcesBashrc = regexp.MustCompile(`(^|[\s;&|])(source|\.)\s+\S*\.bashrc["']?(\s|;|$)`)
	legacyHook    = regexp.MustCompile(`\beval\b.*oci-assume\s+init|(^|[\s;&|])(source|\.)\s+<\(\s*oci-assume\s+init|oci-assume\s+init\b.*\|\s*(source|Out-String|Invoke-Expression|iex)\b|\b(Invoke-Expression|iex)\b.*oci-assume\s+init`)
	heredocStart  = regexp.MustCompile(`(^|[^<])<<-?\s*['"]?([A-Za-z_]\w*)['"]?`)
)

// DetectShell resolves the shell: flag, parent process (not under an installer), $SHELL, then pwsh on windows.
func DetectShell(e *Env, flag string) (string, error) {
	if flag != "" {
		s, err := shell.Parse(flag)
		return string(s), err
	}
	if getenv(e, "OCI_ASSUME_INSTALLER") != "1" {
		if s := normShell(parentName(e)); s != "" {
			return s, nil
		}
	}
	if s := normShell(getenv(e, "SHELL")); s != "" {
		return s, nil
	}
	if e.GOOS == "windows" {
		return "pwsh", nil
	}
	return "", ErrUnknownShell
}

func parentName(e *Env) string {
	if e.GOOS == "windows" {
		p, gp := windowsParent()
		if strings.HasPrefix(baseName(p), "oci-assume") {
			return gp
		}
		return p
	}
	out, _ := run(e, execWait, "ps", "-o", "comm=", "-p", strconv.Itoa(os.Getppid()))
	return out
}

func baseName(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexAny(s, `/\`); i >= 0 {
		s = s[i+1:]
	}
	return strings.ToLower(s)
}

// normShell maps "-zsh", "/bin/bash" or "powershell.exe" to a supported name, else "".
func normShell(s string) string {
	s = strings.TrimSuffix(strings.TrimPrefix(baseName(s), "-"), ".exe")
	switch s {
	case "zsh", "bash", "fish", "pwsh":
		return s
	case "powershell":
		return "pwsh"
	}
	return ""
}

func getenv(e *Env, k string) string {
	if e.Getenv == nil {
		return ""
	}
	return e.Getenv(k)
}

// run calls e.Exec with a bounded context; a nil Exec behaves like a missing command.
func run(e *Env, wait time.Duration, name string, args ...string) (string, error) {
	if e.Exec == nil {
		return "", errors.New("exec unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	return e.Exec(ctx, name, args...)
}

func zshrc(e *Env) string {
	dir := getenv(e, "ZDOTDIR")
	if strings.HasPrefix(dir, "~/") {
		dir = filepath.Join(e.Home, dir[2:])
	}
	if !filepath.IsAbs(dir) {
		dir = e.Home
	}
	return filepath.Join(dir, ".zshrc")
}

func fishConfDir(e *Env) string {
	if x := getenv(e, "XDG_CONFIG_HOME"); filepath.IsAbs(x) {
		return filepath.Join(x, "fish")
	}
	return filepath.Join(e.Home, ".config", "fish")
}

// pwshProfiles asks each installed edition for its AllHosts profile; silent lists editions that gave no path.
func pwshProfiles(e *Env) (paths, silent []string) {
	for _, exe := range []string{"pwsh", "powershell"} {
		if e.LookPath == nil {
			continue
		}
		if _, err := e.LookPath(exe); err != nil {
			continue
		}
		out, err := run(e, pwshWait, exe, "-NoProfile", "-Command", pwshPathQuery)
		p := lastAbsLine(out)
		if err != nil || p == "" {
			silent = append(silent, exe)
			continue
		}
		if !contains(paths, p) {
			paths = append(paths, p)
		}
	}
	return paths, silent
}

func lastAbsLine(s string) string {
	ls := strings.Split(s, "\n")
	for i := len(ls) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(ls[i]), bom)); filepath.IsAbs(l) {
			return l
		}
	}
	return ""
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// HookTargets lists the files the hook step writes for shell, with the block body or drop-in content.
func HookTargets(e *Env, sh string) ([]Target, error) {
	switch sh {
	case "zsh":
		return []Target{hookTarget(e, sh, zshrc(e), "block")}, nil
	case "bash":
		t := []Target{hookTarget(e, sh, filepath.Join(e.Home, ".bashrc"), "block")}
		if e.GOOS == "darwin" {
			if lt, ok := bashLoginTarget(e); ok {
				t = append(t, lt)
			}
		}
		return t, nil
	case "fish":
		return []Target{hookTarget(e, sh, filepath.Join(fishConfDir(e), "conf.d", "oci-assume.fish"), "dropin")}, nil
	case "pwsh":
		ps, _ := pwshProfiles(e)
		if len(ps) == 0 {
			return nil, fmt.Errorf("%w: no PowerShell reported its $PROFILE path", errNoShell)
		}
		var t []Target
		for _, p := range ps {
			t = append(t, hookTarget(e, sh, p, "block"))
		}
		return t, nil
	}
	return nil, fmt.Errorf("unsupported shell %q", sh)
}

// hookTarget adds a PATH line when oci-assume does not resolve to this binary, or when the file already has it.
func hookTarget(e *Env, sh, path, kind string) Target {
	wrap := func(body string) string {
		if kind == "dropin" {
			return fishHeader + body + "\n"
		}
		return body
	}
	t := Target{path, kind, wrap(hookLine(sh))}
	dir, needed := BinDir(e)
	if dir == "" {
		return t
	}
	withPath := Target{path, kind, wrap(pathLine(e, sh, dir) + "\n" + hookLine(sh))}
	if needed {
		return withPath
	}
	if ok, _ := targetDone(withPath); ok {
		return withPath
	}
	return t
}

// BinDir is the running binary's directory; needed is true when `oci-assume` on PATH is not this binary.
// dir is "" when unknown or when the binary is not named oci-assume, since a PATH line would not help.
func BinDir(e *Env) (dir string, needed bool) {
	if e.Executable == nil {
		return "", false
	}
	exe, err := e.Executable()
	if err != nil || exe == "" {
		return "", false
	}
	if n := strings.ToLower(filepath.Base(exe)); n != "oci-assume" && n != "oci-assume.exe" {
		return "", false
	}
	if e.LookPath != nil {
		if p, err := e.LookPath("oci-assume"); err == nil && samePath(e, p, exe) {
			return filepath.Dir(exe), false
		}
	}
	return filepath.Dir(exe), true
}

func samePath(e *Env, a, b string) bool {
	if r, err := filepath.EvalSymlinks(a); err == nil {
		a = r
	}
	if r, err := filepath.EvalSymlinks(b); err == nil {
		b = r
	}
	a, b = filepath.Clean(a), filepath.Clean(b)
	if e.GOOS == "windows" || e.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// pathLine prepends dir to PATH; POSIX shells write a dir under HOME as "$HOME/...".
func pathLine(e *Env, sh, dir string) string {
	switch sh {
	case "fish":
		return `fish_add_path -g "` + homeRel(e, dir, `\`, `"`, `$`) + `"`
	case "pwsh":
		sep, v := ":", "$env:PATH"
		if e.GOOS == "windows" {
			sep, v = ";", "$env:Path"
		}
		q := strings.NewReplacer("`", "``", `"`, "`\"", "$", "`$").Replace(dir)
		return v + ` = "` + q + sep + `" + ` + v
	}
	return `export PATH="` + homeRel(e, dir, `\`, `"`, `$`, "`") + `:$PATH"`
}

// homeRel escapes dir for a double-quoted string, writing a HOME prefix as $HOME.
func homeRel(e *Env, dir string, specials ...string) string {
	var pairs []string
	for _, c := range specials {
		pairs = append(pairs, c, `\`+c)
	}
	esc := strings.NewReplacer(pairs...).Replace
	for _, h := range []string{e.Home, evalOr(e.Home)} {
		for _, d := range []string{dir, evalOr(dir)} {
			if rel, err := filepath.Rel(h, d); h != "" && err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				if rel == "." {
					return "$HOME"
				}
				return "$HOME/" + esc(filepath.ToSlash(rel))
			}
		}
	}
	return esc(dir)
}

func evalOr(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// bashLoginTarget picks the macOS login file that must source .bashrc; ok is false when one already does.
func bashLoginTarget(e *Env) (Target, bool) {
	for _, name := range []string{".bash_profile", ".bash_login"} {
		p := filepath.Join(e.Home, name)
		if !userFile(p) {
			continue
		}
		if fileSourcesBashrc(p) {
			return Target{}, false
		}
		return Target{p, "block", bashProfileLine}, true
	}
	// No login file of the user's: create .bash_profile, keeping ~/.profile in the chain.
	body := bashProfileLine
	if prof := filepath.Join(e.Home, ".profile"); fileExists(prof) {
		body = profileLine
		if !fileSourcesBashrc(prof) {
			body += "\n" + bashProfileLine
		}
	}
	return Target{filepath.Join(e.Home, ".bash_profile"), "block", body}, true
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// userFile reports whether p exists with content beyond our own block.
func userFile(p string) bool {
	b, err := os.ReadFile(p) // #nosec G304 -- fixed rc path under HOME
	if err != nil {
		return false
	}
	rest, err := withoutBlock(string(b), "", false)
	return err != nil || rest != ""
}

func fileSourcesBashrc(p string) bool {
	b, err := os.ReadFile(p) // #nosec G304 -- fixed rc path under HOME
	if err != nil {
		return false
	}
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(strings.TrimPrefix(l, bom))
		if !strings.HasPrefix(l, "#") && sourcesBashrc.MatchString(l) {
			return true
		}
	}
	return false
}

// legacyFiles are where a hand-added, unmarked hook line may live for sh.
func legacyFiles(e *Env, sh string, targets []Target) []string {
	switch sh {
	case "zsh":
		return []string{zshrc(e)}
	case "bash":
		return []string{filepath.Join(e.Home, ".bashrc"), filepath.Join(e.Home, ".bash_profile"), filepath.Join(e.Home, ".bash_login")}
	case "fish":
		return []string{filepath.Join(fishConfDir(e), "config.fish")}
	}
	var out []string
	for _, t := range targets {
		out = append(out, t.Path, filepath.Join(filepath.Dir(t.Path), "Microsoft.PowerShell_profile.ps1"))
	}
	return out
}

// hasLegacyLine reports a live eval/source/pipe of `oci-assume init` outside our markers, comments and heredocs.
func hasLegacyLine(p string) bool { return len(legacyLines(p)) > 0 }

// legacyLines returns the 1-based line numbers of hasLegacyLine's matches.
func legacyLines(p string) []int {
	b, err := os.ReadFile(p) // #nosec G304 -- fixed rc path under HOME
	if err != nil {
		return nil
	}
	var out []int
	in, psComment, heredoc := false, false, ""
	for i, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(strings.TrimPrefix(l, bom))
		switch {
		case heredoc != "":
			if l == heredoc {
				heredoc = ""
			}
		case psComment:
			psComment = !strings.Contains(l, "#>")
		case l == markBegin:
			in = true
		case l == markEnd:
			in = false
		case in:
		case strings.HasPrefix(l, "<#"):
			psComment = !strings.Contains(l[2:], "#>")
		default:
			code := stripComment(l)
			if m := heredocStart.FindStringSubmatch(code); m != nil {
				heredoc = m[2]
				continue
			}
			if legacyHook.MatchString(code) {
				out = append(out, i+1)
			}
		}
	}
	return out
}

// stripComment cuts a shell-style trailing comment that is outside quotes.
func stripComment(l string) string {
	var q byte
	for i := 0; i < len(l); i++ {
		c := l[i]
		switch {
		case q != 0:
			if c == q {
				q = 0
			}
		case c == '\'' || c == '"':
			q = c
		case c == '#' && (i == 0 || l[i-1] == ' ' || l[i-1] == '\t'):
			return l[:i]
		}
	}
	return l
}

type span struct{ start, end int }

// blocks locates every marked block: start of the begin line (after a BOM) to the end of the end line.
func blocks(s string) ([]span, error) {
	var out []span
	cur := -1
	for off := 0; off < len(s); {
		next := len(s)
		if n := strings.IndexByte(s[off:], '\n'); n >= 0 {
			next = off + n + 1
		}
		raw, start := s[off:next], off
		if strings.HasPrefix(raw, bom) {
			raw, start = raw[len(bom):], off+len(bom)
		}
		switch strings.TrimSpace(raw) {
		case markBegin:
			if cur >= 0 {
				return nil, errNested
			}
			cur = start
		case markEnd:
			if cur >= 0 {
				out = append(out, span{cur, next})
				cur = -1
			}
		}
		off = next
	}
	if cur >= 0 {
		return nil, errors.New("oci-assume block has no end marker; fix the file by hand")
	}
	return out, nil
}

func eolOf(s, goos string) string {
	if strings.Contains(s, "\r\n") || (s == "" && goos == "windows") {
		return "\r\n"
	}
	return "\n"
}

func block(body, eol string) string {
	return markBegin + eol + strings.ReplaceAll(body, "\n", eol) + eol + markEnd + eol
}

// cut removes spans from s, putting repl at the first one.
func cut(s string, bs []span, repl string) string {
	var b strings.Builder
	prev := 0
	for i, sp := range bs {
		b.WriteString(s[prev:sp.start])
		if i == 0 {
			b.WriteString(repl)
		}
		prev = sp.end
	}
	b.WriteString(s[prev:])
	return b.String()
}

// withBlock sets our block to body: the first block is replaced and duplicates dropped, else it is appended after a separator EOL.
func withBlock(s, body, goos string) (string, error) {
	eol := eolOf(s, goos)
	b := block(body, eol)
	bs, err := blocks(s)
	switch {
	case err != nil:
		return "", err
	case len(bs) > 0:
		return cut(s, bs, b), nil
	case s == "":
		return b, nil
	}
	return s + eol + b, nil
}

// withoutBlock removes every block and the blank separator line before it; at EOF it also drops
// the EOL withBlock added to a file without a trailing newline (it then matches the backup).
func withoutBlock(s, bak string, hasBak bool) (string, error) {
	bs, err := blocks(s)
	if err != nil || len(bs) == 0 {
		return s, err
	}
	eol := eolOf(s, "")
	atEOF := bs[len(bs)-1].end == len(s)
	for i := range bs {
		if strings.HasSuffix(s[:bs[i].start], eol+eol) {
			bs[i].start -= len(eol)
		}
	}
	out := cut(s, bs, "")
	if !atEOF {
		return out, nil
	}
	if trimmed, ok := strings.CutSuffix(out, eol); ok && hasBak && trimmed == bak {
		return trimmed, nil
	}
	return out, nil
}

// resolve follows symlinks so dotfile managers keep their links; a missing file resolves to itself.
func resolve(p string) (string, error) {
	r, err := filepath.EvalSymlinks(p)
	if err == nil {
		return r, nil
	}
	if isLink(p) {
		return "", fmt.Errorf("%s is a broken symlink: %w", p, err)
	}
	if errors.Is(err, fs.ErrNotExist) {
		return p, nil
	}
	return "", err
}

func isLink(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.Mode()&fs.ModeSymlink != 0
}

// readRC returns the resolved path, content, mode and whether the file exists.
func readRC(p string) (real, s string, mode fs.FileMode, exists bool, err error) {
	if real, err = resolve(p); err != nil {
		return "", "", 0, false, err
	}
	b, err := os.ReadFile(real) // #nosec G304 -- rc path from HookTargets or the receipt
	if errors.Is(err, fs.ErrNotExist) {
		return real, "", 0o644, false, nil
	}
	if err != nil {
		return "", "", 0, false, err
	}
	fi, err := os.Stat(real)
	if err != nil {
		return "", "", 0, false, err
	}
	return real, string(b), fi.Mode().Perm(), true, nil
}

// writeRC backs up the user's content once to <path>.oci-assume.bak, then writes atomically with the old mode.
func writeRC(p, real, old, s string, mode fs.FileMode, exists bool) error {
	if exists {
		if mode&0o200 == 0 {
			return fmt.Errorf("%s is read-only; add the oci-assume block by hand or make it writable", p)
		}
		// Only the pre-setup state is worth keeping: skip when a backup exists or old is just our block.
		if rest, _ := withoutBlock(old, "", false); !fileExists(p+bakSuffix) && (rest != "" || old == "") {
			if err := atomicWrite(p+bakSuffix, old, mode); err != nil {
				return fmt.Errorf("back up %s: %w", p, err)
			}
		}
	}
	return atomicWrite(real, s, mode)
}

func atomicWrite(p, s string, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".oci-assume-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, werr := tmp.WriteString(s)
	serr := tmp.Sync()
	cerr := tmp.Close()
	if err := errors.Join(werr, serr, cerr, os.Chmod(name, mode)); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, p); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

// targetDone reports whether t holds exactly one block (or drop-in) with our content.
func targetDone(t Target) (bool, error) {
	_, s, _, exists, err := readRC(t.Path)
	if err != nil || !exists {
		return false, err
	}
	if t.Kind == "dropin" {
		return s == t.Content, nil
	}
	bs, err := blocks(s)
	if err != nil || len(bs) != 1 {
		return false, err
	}
	got := strings.ReplaceAll(s[bs[0].start:bs[0].end], "\r\n", "\n")
	return got == block(t.Content, "\n"), nil
}

// applyTarget writes one target; it is a no-op when the content is already current.
func applyTarget(e *Env, t Target) error {
	real, s, mode, exists, err := readRC(t.Path)
	if err != nil {
		return err
	}
	next := t.Content
	if t.Kind == "block" {
		if next, err = withBlock(s, t.Content, e.GOOS); err != nil {
			return fmt.Errorf("%s: %w", t.Path, err)
		}
	}
	if exists && next == s {
		return nil
	}
	if t.Kind == "dropin" {
		return atomicWrite(real, next, 0o644)
	}
	return writeRC(t.Path, real, s, next, mode, exists)
}

// undoFile removes our blocks or the drop-in and deletes the backup; a file setup created is deleted
// (emptied if it is a symlink). It returns what it did for the uninstall report, or "".
func undoFile(e *Env, f FileEntry) (string, error) {
	if f.Kind == "dropin" {
		return removeOrEmpty(e, f.Path)
	}
	real, s, mode, exists, err := readRC(f.Path)
	if err != nil || !exists {
		return "", err
	}
	bak, berr := os.ReadFile(f.Path + bakSuffix) // #nosec G304 -- our own backup path
	hasBak := berr == nil
	next, err := withoutBlock(s, string(bak), hasBak)
	if err != nil {
		return "", fmt.Errorf("%s: %w", f.Path, err)
	}
	if next == s {
		return "", nil
	}
	if mode&0o200 == 0 {
		return "", fmt.Errorf("%s is read-only; remove the oci-assume block by hand", f.Path)
	}
	var did string
	if next == "" && !hasBak {
		did, err = removeOrEmpty(e, f.Path)
	} else {
		did, err = "Removed the oci-assume block from "+tilde(e, f.Path), atomicWrite(real, next, mode)
	}
	if err != nil {
		return "", err
	}
	if err := os.Remove(f.Path + bakSuffix); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return did, err
	}
	return did, nil
}

// removeOrEmpty deletes p, but never through a symlink: a linked target is emptied instead.
func removeOrEmpty(e *Env, p string) (string, error) {
	if isLink(p) {
		real, s, mode, exists, err := readRC(p)
		if err != nil || !exists || s == "" {
			return "", err
		}
		return "Emptied " + tilde(e, real) + " (linked from " + tilde(e, p) + ")", atomicWrite(real, "", mode)
	}
	err := os.Remove(p)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return "Deleted " + tilde(e, p), nil
}
