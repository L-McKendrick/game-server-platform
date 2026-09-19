package hostdelivery

import "testing"

func TestRuntimeContractRejectsMissingAndLegacyVersions(t *testing.T) {
	for _, version := range []string{"", "steam-auth-broker-v2", RuntimeConfigurationVersion} {
		t.Run(version, func(t *testing.T) {
			t.Setenv("BOOTSTRAP_RUNTIME_CONFIGURATION_VERSION", version)
			if (ValidateRuntimeConfiguration() == nil) != (version == RuntimeConfigurationVersion) {
				t.Fatal("runtime drift not rejected")
			}
		})
	}
}
