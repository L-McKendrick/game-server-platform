package main

import (
	"strings"
	"testing"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/ssmbootstrap"
)

func TestValidateRuntimeConfigurationVersion(t *testing.T) {
	t.Setenv("BOOTSTRAP_RUNTIME_CONFIGURATION_VERSION", ssmbootstrap.RuntimeConfigurationVersion)
	if err := validateRuntimeConfigurationVersion(); err != nil {
		t.Fatalf("validateRuntimeConfigurationVersion() error = %v", err)
	}
}

func TestValidateRuntimeConfigurationVersionRejectsDeploymentDrift(t *testing.T) {
	t.Setenv("BOOTSTRAP_RUNTIME_CONFIGURATION_VERSION", "legacy-password-v1")
	err := validateRuntimeConfigurationVersion()
	if err == nil || !strings.Contains(err.Error(), "steam-auth-cache-v1") || !strings.Contains(err.Error(), "legacy-password-v1") {
		t.Fatalf("validateRuntimeConfigurationVersion() error = %v", err)
	}
}
