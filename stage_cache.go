package main

// Cached stage reuse never establishes what GitHub currently considers the
// latest Stable release. It only validates an existing, previously verified
// artifact in the installer's own temporary stage. No installed files are read
// or modified by this lookup.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func validExternalCache(root, component string, allowTestURLs bool) (preparedExternal, string, bool) {
	empty := preparedExternal{}
	label, prefix, payload := "DDS Plugin", pluginPrefix, "DDS.plugin.js"
	if component == "betterdiscord" {
		label, prefix, payload = "BetterDiscord CLI", bdcliPrefix, "bdcli.exe"
	} else if component != "plugin" {
		return empty, "", false
	}
	parent := filepath.Join(root, component)
	if requireNormalDir(parent) != nil {
		return empty, "", false
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		return empty, "", false
	}
	var tags []string
	for _, ent := range entries {
		tag := ent.Name()
		if !ent.IsDir() || !finalVersionRE.MatchString(strings.TrimPrefix(tag, "v")) {
			continue
		}
		tags = append(tags, tag)
	}
	sort.Slice(tags, func(i, j int) bool {
		return compareStableVersions(strings.TrimPrefix(tags[i], "v"), strings.TrimPrefix(tags[j], "v")) > 0
	})
	for _, tag := range tags {
		version := strings.TrimPrefix(tag, "v")
		target := filepath.Join(parent, tag)
		if requireNormalDir(target) != nil || requireNormalFile(filepath.Join(target, "stage_receipt.json")) != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(target, "stage_receipt.json"))
		if err != nil {
			continue
		}
		var r preparedExternal
		if json.Unmarshal(data, &r) != nil || r.Schema != "dds-installer-external-stage-v1" || r.Status != "VERIFIED_AND_STAGED" || r.Component != label || r.Version != version || !hexDigestRE.MatchString(r.SHA256) || !hexDigestRE.MatchString(r.PayloadSHA256) || r.ZIPSize <= 0 || r.ZIPSize > externalMaxSize {
			continue
		}
		packageName := "DDS.plugin.js"
		if component == "betterdiscord" {
			packageName = "bdcli_" + version + "_windows_amd64.zip"
		}
		if !allowTestURLs && r.Source != prefix+tag+"/"+packageName {
			continue
		}
		pkg := filepath.Join(target, "package", packageName)
		extracted := filepath.Join(target, "payload", payload)
		if requireNormalDir(filepath.Join(target, "package")) != nil || requireNormalDir(filepath.Join(target, "payload")) != nil || requireNormalFile(pkg) != nil || requireNormalFile(extracted) != nil {
			continue
		}
		actual, size, err := shaFile(pkg)
		if err != nil || actual != r.SHA256 || size != r.ZIPSize {
			continue
		}
		if _, ok := readExternalReceipt(target, label, version, r.SHA256, packageName); !ok {
			continue
		}
		if component == "plugin" {
			if r.PayloadSHA256 != r.SHA256 {
				continue
			}
			b, er := os.ReadFile(extracted)
			if er != nil {
				continue
			}
			match := pluginHeaderVersionRE.FindSubmatch(b)
			if len(match) != 2 || string(match[1]) != version {
				continue
			}
		} else if verifyWindowsPE(extracted) != nil {
			continue
		}
		return r, target, true
	}
	return empty, "", false
}

func validCompanionCache(root string, allowTestURLs bool) (stageReceipt, string, bool) {
	empty := stageReceipt{}
	parent := filepath.Join(root, "companion")
	if requireNormalDir(parent) != nil {
		return empty, "", false
	}
	dirs, err := os.ReadDir(parent)
	if err != nil {
		return empty, "", false
	}
	var tags []string
	for _, d := range dirs {
		if d.IsDir() && strings.HasPrefix(d.Name(), "v") && finalVersionRE.MatchString(strings.TrimPrefix(d.Name(), "v")) {
			tags = append(tags, d.Name())
		}
	}
	sort.Slice(tags, func(i, j int) bool {
		return compareStableVersions(strings.TrimPrefix(tags[i], "v"), strings.TrimPrefix(tags[j], "v")) > 0
	})
	for _, tag := range tags {
		version := strings.TrimPrefix(tag, "v")
		target := filepath.Join(parent, tag)
		if requireNormalDir(target) != nil || requireNormalFile(filepath.Join(target, "stage_receipt.json")) != nil || requireNormalDir(filepath.Join(target, "payload")) != nil {
			continue
		}
		content, err := os.ReadFile(filepath.Join(target, "stage_receipt.json"))
		if err != nil {
			continue
		}
		var r stageReceipt
		if json.Unmarshal(content, &r) != nil || r.Schema != "dds-installer-stage-v1" || r.Status != "VERIFIED_AND_STAGED" || r.Component != "DDS Companion" || r.Version != version || !hexDigestRE.MatchString(r.SHA256) || r.ZIPSize <= 0 || r.ZIPSize > maxFullZIP || r.Files <= 0 || r.Files > maxMembers {
			continue
		}
		if !allowTestURLs && r.Source != companionAssetsPrefix+tag+"/DDS-"+version+"-windows-x64.zip" {
			continue
		}
		if validCompanionStageContents(target, r) != nil {
			continue
		}
		return r, target, true
	}
	return empty, "", false
}

// Bootstrap files are outside the versioned DDS.install.json manifest. The
// hashes are captured only after authenticating the downloaded Release ZIP.
// A legacy receipt with no hashes is deliberately not a validated cache.
func bootstrapPaths(version string) []string {
	return []string{"DDS.exe", "DDSUpdater.exe", "current.json", filepath.ToSlash(filepath.Join("versions", version, "DDS.install.json"))}
}

func collectBootstrapSHA256(payload, version string) (map[string]string, error) {
	out := make(map[string]string, 4)
	for _, name := range bootstrapPaths(version) {
		p := filepath.Join(payload, filepath.FromSlash(name))
		if err := requireNormalFile(p); err != nil {
			return nil, fmt.Errorf("bootstrap %s: %w", name, err)
		}
		hash, _, err := shaFile(p)
		if err != nil {
			return nil, err
		}
		out[name] = hash
	}
	return out, nil
}

func validCompanionStageContents(target string, r stageReceipt) error {
	if len(r.BootstrapSHA256) != 4 {
		return errors.New("legacy staging не содержит SHA-256 корневых файлов; требуется повторная проверенная подготовка")
	}
	payload := filepath.Join(target, "payload")
	if requireNormalDir(payload) != nil {
		return errors.New("payload отсутствует")
	}
	for _, name := range bootstrapPaths(r.Version) {
		expected := r.BootstrapSHA256[name]
		if !hexDigestRE.MatchString(expected) {
			return fmt.Errorf("нет проверенного SHA-256 для %s", name)
		}
		p := filepath.Join(payload, filepath.FromSlash(name))
		if err := requireNormalFile(p); err != nil {
			return fmt.Errorf("unsafe bootstrap %s: %w", name, err)
		}
		actual, _, err := shaFile(p)
		if err != nil || actual != expected {
			return fmt.Errorf("bootstrap SHA-256 mismatch: %s", name)
		}
	}
	// Internal files are still fully checked against the versioned manifest.
	return validateCompanionPackage(payload, r.Version)
}
