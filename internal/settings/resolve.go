package settings

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Source says where a resolved value came from, so create output can name it.
type Source string

const (
	SourceFlag   Source = "flag"
	SourceConfig Source = "~/.stoat/config.toml"
	SourceGcloud Source = "gcloud's active config"
)

// Resolved is a project and zone pair together with where they came from.
type Resolved struct {
	Project string
	Zone    string
	Source  Source
}

// ResolveGCE picks a GCP project and zone: the flag, then config.toml, then
// gcloud's active configuration read from disk. Project and zone resolve
// together from whichever source supplies both, so a project from config.toml
// and a zone from gcloud never mix into one Resolved.
func ResolveGCE(flagProject, flagZone string) (Resolved, error) {
	cfg, err := Load()
	if err != nil {
		return Resolved{}, err
	}
	gp, gz, _ := gcloudActiveConfig()

	project, source := flagProject, SourceFlag
	if project == "" {
		project, source = cfg.Providers.GCE.Project, SourceConfig
	}
	if project == "" {
		project, source = gp, SourceGcloud
	}

	zone, zoneSource := flagZone, SourceFlag
	if zone == "" {
		zone, zoneSource = cfg.Providers.GCE.Zone, SourceConfig
	}
	if zone == "" {
		zone, zoneSource = gz, SourceGcloud
	}

	// The source reported is whichever field fell furthest down the
	// precedence chain, so create output never claims a flag value that
	// only one of the two fields actually had.
	if sourceRank(zoneSource) > sourceRank(source) {
		source = zoneSource
	}

	if project != "" && zone != "" {
		return Resolved{Project: project, Zone: zone, Source: source}, nil
	}

	var missing []string
	if project == "" {
		missing = append(missing, "no project: pass --project, set providers.gce.project in config.toml, or run gcloud config set project")
	}
	if zone == "" {
		missing = append(missing, "no zone: pass --zone, set providers.gce.zone in config.toml, or run gcloud config set compute/zone")
	}
	return Resolved{}, fmt.Errorf("%s", strings.Join(missing, "; "))
}

func sourceRank(s Source) int {
	switch s {
	case SourceFlag:
		return 0
	case SourceConfig:
		return 1
	default:
		return 2
	}
}

// gcloudConfigDir is CLOUDSDK_CONFIG, defaulting to gcloud's own default.
func gcloudConfigDir() string {
	if d := os.Getenv("CLOUDSDK_CONFIG"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".config", "gcloud")
	}
	return filepath.Join(home, ".config", "gcloud")
}

// gcloudActiveConfig reads project and zone from gcloud's active
// configuration file without invoking the gcloud binary.
func gcloudActiveConfig() (project, zone string, err error) {
	dir := gcloudConfigDir()
	active := "default"
	if b, err := os.ReadFile(filepath.Join(dir, "active_config")); err == nil {
		if s := strings.TrimSpace(string(b)); s != "" {
			active = s
		}
	}
	f, err := os.Open(filepath.Join(dir, "configurations", "config_"+active))
	if err != nil {
		return "", "", err
	}
	defer f.Close()

	section := ""
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		switch {
		case section == "core" && key == "project":
			project = val
		case section == "compute" && key == "zone":
			zone = val
		}
	}
	if err := scanner.Err(); err != nil {
		return "", "", err
	}
	return project, zone, nil
}
