package updater

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
)

type Source interface {
	Releases(context.Context) ([]Release, error)
	Download(context.Context, Plan, string) error
	Commit(context.Context, string) (string, error)
}
type GH struct{ Executable string }

func ExistingGH() (*GH, error) {
	path, e := exec.LookPath("gh")
	if e != nil && runtime.GOOS == "darwin" {
		for _, candidate := range []string{"/opt/homebrew/bin/gh", "/usr/local/bin/gh"} {
			if p, err := exec.LookPath(candidate); err == nil {
				path = p
				e = nil
				break
			}
		}
	}
	if e != nil {
		return nil, ErrAccess
	}
	return &GH{path}, nil
}

type cappedWriter struct {
	W         io.Writer
	Remaining int64
}

func (w *cappedWriter) Write(b []byte) (int, error) {
	if int64(len(b)) > w.Remaining {
		return 0, errors.New("GitHub output exceeds bound")
	}
	n, e := w.W.Write(b)
	w.Remaining -= int64(n)
	return n, e
}
func (g *GH) run(ctx context.Context, out io.Writer, limit int64, args ...string) error {
	cmd := exec.CommandContext(ctx, g.Executable, args...)
	quietCommand(cmd)
	cmd.Env = append(os.Environ(), "GH_HOST=github.com", "GH_PROMPT_DISABLED=1", "GH_PAGER=cat", "NO_COLOR=1")
	cmd.Stdout = &cappedWriter{out, limit}
	cmd.Stderr = &cappedWriter{io.Discard, 8192}
	if cmd.Run() != nil {
		return ErrAccess
	}
	return nil
}
func (g *GH) api(ctx context.Context, path string, out any) error {
	var b bytes.Buffer
	if e := g.run(ctx, &b, 2<<20, "api", "--hostname", "github.com", "-H", "Accept: application/vnd.github+json", path); e != nil {
		return e
	}
	if json.Unmarshal(b.Bytes(), out) != nil {
		return errors.New("invalid GitHub metadata")
	}
	return nil
}
func (g *GH) Releases(ctx context.Context) ([]Release, error) {
	var repo struct {
		ID       int64
		FullName string `json:"full_name"`
		Private  bool
	}
	if e := g.api(ctx, "repos/"+BuildConfig().Repository, &repo); e != nil {
		return nil, e
	}
	if repo.ID != BuildConfig().RepositoryID || repo.FullName != BuildConfig().Repository || repo.Private {
		return nil, errors.New("repository identity/visibility changed; updates refused")
	}
	var rs []Release
	e := g.api(ctx, "repos/"+BuildConfig().Repository+"/releases?per_page=100", &rs)
	return rs, e
}
func (g *GH) Commit(ctx context.Context, tag string) (string, error) {
	if len(tag) < 2 || tag[0] != 'v' {
		return "", errors.New("invalid release tag")
	}
	if _, e := ParseVersion(tag[1:]); e != nil {
		return "", errors.New("invalid release tag")
	}
	var c struct{ SHA string }
	if e := g.api(ctx, "repos/"+BuildConfig().Repository+"/commits/"+tag, &c); e != nil {
		return "", e
	}
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(c.SHA) {
		return "", errors.New("invalid release commit")
	}
	return c.SHA, nil
}
func (g *GH) Download(ctx context.Context, p Plan, path string) error {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	e = g.run(ctx, f, p.Asset.Size, "api", "--hostname", "github.com", "-H", "Accept: application/octet-stream", "repos/"+BuildConfig().Repository+"/releases/assets/"+strconv.FormatInt(p.Asset.ID, 10))
	closeErr := f.Close()
	if e != nil {
		return e
	}
	return closeErr
}
func safeGHVersion(text string) bool {
	m := regexp.MustCompile(`^gh version ([0-9]+)\.([0-9]+)\.([0-9]+)(?:[ (\r\n]|$)`).FindStringSubmatch(text)
	if m == nil {
		return false
	}
	v, err := ParseVersion(m[1] + "." + m[2] + "." + m[3])
	if err != nil {
		return false
	}
	min, _ := ParseVersion("2.93.0")
	return v.Compare(min) >= 0
}
func (g *GH) Verify(ctx context.Context, tag, path string) error {
	var version bytes.Buffer
	if g.run(ctx, &version, 8192, "--version") != nil || !safeGHVersion(version.String()) {
		return errors.New("automatic apply requires existing GitHub CLI 2.93.0+ (GHSA-8xvp-7hj6-mcj9); no software is installed automatically")
	}
	var b bytes.Buffer
	// Supported GitHub/Sigstore release attestation verification, never a home-grown signature.
	if g.run(ctx, &b, 2<<20, "release", "verify-asset", tag, path, "--repo", "github.com/"+BuildConfig().Repository, "--format", "json") != nil {
		return ErrProvenance
	}
	if !json.Valid(b.Bytes()) || len(bytes.TrimSpace(b.Bytes())) < 3 {
		return ErrProvenance
	}
	return nil
}
