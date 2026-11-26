// Copyright (2026) Christophe Pallier <christophe@pallier.org>
// Licensed under the Apache License, Version 2.0 (see LICENSE.txt).

package main

import (
	"runtime/debug"
	"strings"
)

// versionString is the version of the running program, from the build info
// that go build embeds: the module path and version (the git tag, or a
// pseudo-version, with +dirty for uncommitted changes) and the git commit.
// go run records no commit, which leaves "<path> (devel)".
func versionString() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "(unknown)"
	}
	return formatVersion(info)
}

func formatVersion(info *debug.BuildInfo) string {
	path, version := info.Main.Path, info.Main.Version
	if path == "" {
		path = info.Path
	}
	if path == "" {
		path = "(unknown)"
	}
	if version == "" {
		version = "(devel)"
	}
	var revision, vcsTime string
	modified := false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.time":
			vcsTime = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if revision == "" {
		return path + " " + version
	}
	if len(revision) > 12 {
		revision = revision[:12]
	}
	details := []string{"commit " + revision}
	if vcsTime != "" {
		details = append(details, vcsTime)
	}
	if modified && !strings.HasSuffix(version, "+dirty") {
		details = append(details, "modified")
	}
	return path + " " + version + " (" + strings.Join(details, ", ") + ")"
}
