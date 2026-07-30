package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	backendImageKey  = "AGENT_COMPOSE_IMAGE"
	frontendImageKey = "AGENT_COMPOSE_FRONTEND_IMAGE"
	guestImageKey    = "DEFAULT_IMAGE"
)

// ImageSource identifies why an image will be effective after an operation.
type ImageSource string

const (
	ImageSourceRelease   ImageSource = "release"
	ImageSourceOverride  ImageSource = "override"
	ImageSourcePreserved ImageSource = "preserved"
)

// ImageSelection is an effective image reference and the policy that chose it.
type ImageSelection struct {
	Value  string
	Source ImageSource
}

// ImagePreview contains the effective images for a proposed operation.
type ImagePreview struct {
	Backend  ImageSelection
	Frontend ImageSelection
	Guest    ImageSelection
}

// ImageReferences contains the three image defaults published by a release.
type ImageReferences struct {
	Backend  string
	Frontend string
	Guest    string
}

// Release owns a verified deployment bundle and its image defaults.
type Release struct {
	bundle  *bundle
	version string
	Images  ImageReferences
}

// Close releases temporary files owned by the resolved release.
func (r *Release) Close() {
	if r == nil || r.bundle == nil {
		return
	}
	r.bundle.Close()
	r.bundle = nil
}

// ResolveRelease loads and verifies the selected release without modifying an
// installation. The caller must close the returned release.
func (s Service) ResolveRelease(ctx context.Context, options Options) (*Release, error) {
	loaded, err := (bundleLoader{client: s.HTTPClient}).Load(ctx, options)
	if err != nil {
		return nil, err
	}
	release := &Release{bundle: loaded, version: options.Version}
	envData, err := os.ReadFile(filepath.Join(loaded.Dir, ".env.example"))
	if err != nil {
		release.Close()
		return nil, fmt.Errorf("read release environment defaults: %w", err)
	}
	env := parseEnvFile(envData)
	state := parseEnvFile(nil)
	defaultOptions := options
	defaultOptions.BackendImage, defaultOptions.BackendImageSet = "", false
	defaultOptions.FrontendImage, defaultOptions.FrontendImageSet = "", false
	defaultOptions.GuestImage, defaultOptions.GuestImageSet = "", false
	plan := planImageReferences(env, state, loaded.Manifest, defaultOptions, "install")
	if err := applyPlannedImageReferences(env, state, plan); err != nil {
		release.Close()
		return nil, err
	}
	release.Images = imageReferences(env)
	return release, nil
}

// PreviewImages reports the values that the selected release would make
// effective, including existing operator-owned values preserved on upgrade.
func (s Service) PreviewImages(operation Operation, options Options, release *Release) (ImagePreview, error) {
	if release == nil || release.bundle == nil {
		return ImagePreview{}, fmt.Errorf("resolved release is required")
	}
	envData, envExists, err := readOptionalFile(filepath.Join(options.InstallDir, ".env"))
	if err != nil {
		return ImagePreview{}, err
	}
	if !envExists {
		envData, err = os.ReadFile(filepath.Join(release.bundle.Dir, ".env.example"))
		if err != nil {
			return ImagePreview{}, fmt.Errorf("read release environment defaults: %w", err)
		}
	}
	stateData, _, err := readOptionalFile(filepath.Join(options.InstallDir, ".installer-state.env"))
	if err != nil {
		return ImagePreview{}, err
	}
	mode := "set-missing"
	if !envExists {
		mode = "install"
	} else if operation == OperationUpgrade {
		mode = "upgrade"
	}
	plan := planImageReferences(parseEnvFile(envData), parseEnvFile(stateData), release.bundle.Manifest, options, mode)
	return ImagePreview{
		Backend:  plan.selection(backendImageKey),
		Frontend: plan.selection(frontendImageKey),
		Guest:    plan.selection(guestImageKey),
	}, nil
}

type plannedImage struct {
	value     string
	set       bool
	selection ImageSelection
}

type imageReferencePlan map[string]plannedImage

func (p imageReferencePlan) selection(key string) ImageSelection { return p[key].selection }

func planImageReferences(env, state, manifest *envFile, options Options, mode string) imageReferencePlan {
	desired := desiredImageReferences(manifest, options)
	explicit := map[string]bool{
		backendImageKey:  options.BackendImageSet,
		frontendImageKey: options.FrontendImageSet,
		guestImageKey:    options.GuestImageSet,
	}
	plan := imageReferencePlan{}
	for _, key := range []string{backendImageKey, "AGENT_COMPOSE_FRONTEND_VERSION", frontendImageKey, guestImageKey} {
		value, desiredExists := desired[key]
		current, currentExists := env.Get(key)
		managed, managedExists := state.Get(key)
		shouldSet := explicit[key] || mode == "install" || !currentExists || current == ""
		if mode == "upgrade" && managedExists && current == managed {
			shouldSet = true
		}
		item := plannedImage{value: value, set: desiredExists && shouldSet}
		switch {
		case item.set && explicit[key]:
			item.selection = ImageSelection{Value: value, Source: ImageSourceOverride}
		case item.set:
			item.selection = ImageSelection{Value: value, Source: ImageSourceRelease}
		case mode == "install" && currentExists:
			item.selection = ImageSelection{Value: strings.TrimSpace(current), Source: ImageSourceRelease}
		case currentExists:
			item.selection = ImageSelection{Value: strings.TrimSpace(current), Source: ImageSourcePreserved}
		}
		plan[key] = item
	}
	return plan
}

func desiredImageReferences(manifest *envFile, options Options) map[string]string {
	desired := map[string]string{}
	if options.ImagePrefix != "" {
		version := options.Version
		if image, ok := manifest.Get(backendImageKey); ok {
			if colon := strings.LastIndex(image, ":"); colon > strings.LastIndex(image, "/") {
				version = image[colon+1:]
			}
		}
		frontendVersion := options.FrontendVersion
		if frontendVersion == "" {
			frontendVersion = DefaultVersion
		}
		desired[backendImageKey] = options.ImagePrefix + "/agent-compose:" + version
		desired["AGENT_COMPOSE_FRONTEND_VERSION"] = frontendVersion
		desired[frontendImageKey] = options.ImagePrefix + "/agent-compose-ui:" + frontendVersion
		desired[guestImageKey] = options.ImagePrefix + "/agent-compose-guest:" + version
	} else {
		for _, key := range []string{backendImageKey, "AGENT_COMPOSE_FRONTEND_VERSION", frontendImageKey, guestImageKey} {
			if value, ok := manifest.Get(key); ok {
				desired[key] = value
			}
		}
	}
	if options.BackendImageSet {
		desired[backendImageKey] = strings.TrimSpace(options.BackendImage)
	}
	if options.FrontendImageSet {
		desired[frontendImageKey] = strings.TrimSpace(options.FrontendImage)
	}
	if options.GuestImageSet {
		desired[guestImageKey] = strings.TrimSpace(options.GuestImage)
	}
	return desired
}

func applyImageReferences(env, state, manifest *envFile, options Options, mode string) error {
	return applyPlannedImageReferences(env, state, planImageReferences(env, state, manifest, options, mode))
}

func applyPlannedImageReferences(env, state *envFile, plan imageReferencePlan) error {
	for key, item := range plan {
		if !item.set {
			continue
		}
		if err := env.Set(key, item.value); err != nil {
			return err
		}
		if err := state.Set(key, item.value); err != nil {
			return err
		}
	}
	return state.Set("INSTALLER_PAYLOAD_VERSION", "1")
}

func imageReferences(env *envFile) ImageReferences {
	backend, _ := env.Get(backendImageKey)
	frontend, _ := env.Get(frontendImageKey)
	guest, _ := env.Get(guestImageKey)
	return ImageReferences{Backend: strings.TrimSpace(backend), Frontend: strings.TrimSpace(frontend), Guest: strings.TrimSpace(guest)}
}
