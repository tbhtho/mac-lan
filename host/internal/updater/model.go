package updater

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	ErrNoUpdate   = errors.New("no newer compatible release in this channel")
	ErrProvenance = errors.New("signed GitHub release provenance unavailable; automatic apply is blocked, staged manual review only")
	ErrAccess     = errors.New("GitHub unavailable or private-repo access missing; use an existing authenticated GitHub CLI or manually download the private release; MAC-DOW will not sign in or save credentials")
)

//go:embed config.json
var configFS embed.FS

type Config struct {
	Version        string
	Repository     string
	RepositoryID   int64
	DefaultChannel string
}

func BuildConfig() Config {
	b, _ := configFS.ReadFile("config.json")
	var c Config
	if json.Unmarshal(b, &c) != nil || c.Repository != "tbhtho/MAC-DOW" || c.RepositoryID != 1430459188 {
		panic("invalid build identity")
	}
	return c
}

type Asset struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}
type Release struct {
	ID         int64   `json:"id"`
	Tag        string  `json:"tag_name"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Assets     []Asset `json:"assets"`
}
type Plan struct {
	Tag          string `json:"tag"`
	Version      string `json:"version"`
	Channel      string `json:"channel"`
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	ReleaseID    int64  `json:"releaseId"`
	RepositoryID int64  `json:"repositoryId"`
	Commit       string `json:"commit"`
	Asset        Asset  `json:"asset"`
}
type Version struct {
	Major, Minor, Patch uint64
	Pre                 []string
}

var semver = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)

func ParseVersion(s string) (Version, error) {
	var v Version
	m := semver.FindStringSubmatch(s)
	if m == nil {
		return v, errors.New("invalid canonical version")
	}
	var err error
	for i, p := range []*uint64{&v.Major, &v.Minor, &v.Patch} {
		*p, err = strconv.ParseUint(m[i+1], 10, 64)
		if err != nil {
			return v, err
		}
	}
	if m[4] != "" {
		v.Pre = strings.Split(m[4], ".")
		for _, part := range v.Pre {
			numeric := strings.Trim(part, "0123456789") == ""
			if numeric {
				if _, err = strconv.ParseUint(part, 10, 64); err != nil {
					return v, errors.New("numeric prerelease overflow")
				}
				if len(part) > 1 && part[0] == '0' {
					return v, errors.New("numeric prerelease has leading zero")
				}
			}
		}
	}
	return v, nil
}
func (v Version) Compare(b Version) int {
	for i, a := range []uint64{v.Major, v.Minor, v.Patch} {
		x := []uint64{b.Major, b.Minor, b.Patch}[i]
		if a < x {
			return -1
		}
		if a > x {
			return 1
		}
	}
	if len(v.Pre) == 0 && len(b.Pre) == 0 {
		return 0
	}
	if len(v.Pre) == 0 {
		return 1
	}
	if len(b.Pre) == 0 {
		return -1
	}
	for i, a := range v.Pre {
		if i >= len(b.Pre) {
			return 1
		}
		x := b.Pre[i]
		if a == x {
			continue
		}
		an, ae := strconv.ParseUint(a, 10, 64)
		bn, be := strconv.ParseUint(x, 10, 64)
		if ae == nil && be == nil {
			if an < bn {
				return -1
			}
			return 1
		}
		if ae == nil {
			return -1
		}
		if be == nil {
			return 1
		}
		if a < x {
			return -1
		}
		return 1
	}
	if len(v.Pre) < len(b.Pre) {
		return -1
	}
	return 0
}
func assetName(goos, arch string) (string, error) {
	if goos == "windows" && arch == "amd64" {
		return "MAC-DOW-windows-x64-unsigned.zip", nil
	}
	if goos == "darwin" && arch == "arm64" {
		return "MAC-DOW-macos-arm64-unsigned.tar.gz", nil
	}
	return "", errors.New("unsupported update platform/architecture")
}
func Select(releases []Release, current, channel, goos, arch string) (Plan, error) {
	var result Plan
	cv, err := ParseVersion(current)
	if err != nil {
		return result, err
	}
	if channel != "stable" && channel != "preview" {
		return result, errors.New("unknown update channel")
	}
	name, err := assetName(goos, arch)
	if err != nil {
		return result, err
	}
	best := cv
	for _, r := range releases {
		if r.Draft || r.ID <= 0 || !strings.HasPrefix(r.Tag, "v") {
			continue
		}
		v, e := ParseVersion(strings.TrimPrefix(r.Tag, "v"))
		if e != nil || r.Prerelease != (len(v.Pre) > 0) || channel == "stable" && r.Prerelease || v.Compare(best) <= 0 {
			continue
		}
		var chosen *Asset
		duplicate := false
		for i := range r.Assets {
			a := &r.Assets[i]
			if a.Name == name {
				if chosen != nil {
					duplicate = true
				}
				chosen = a
			}
		}
		if chosen == nil || duplicate || chosen.ID <= 0 || chosen.Size <= 0 || chosen.Size > MaxArchive || !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(chosen.Digest) {
			continue
		}
		best = v
		result = Plan{Tag: r.Tag, Version: strings.TrimPrefix(r.Tag, "v"), Channel: channel, OS: goos, Arch: arch, ReleaseID: r.ID, RepositoryID: BuildConfig().RepositoryID, Asset: *chosen}
	}
	if result.Tag == "" {
		return result, ErrNoUpdate
	}
	return result, nil
}
func (p Plan) confirmation() string { return fmt.Sprintf("APPLY %s", p.Tag) }
