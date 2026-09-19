package hostdelivery

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/hostaccess"
	"github.com/L-McKendrick/game-server-platform/internal/app"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type CommandRecords interface {
	app.HostAccessRecords
	ports.HostAccessAttemptRepository
}

const RuntimeConfigurationVersion = domain.HostAccessRuntimeConfigurationVersion

func ValidateRuntimeConfiguration() error {
	if strings.TrimSpace(os.Getenv("BOOTSTRAP_RUNTIME_CONFIGURATION_VERSION")) != RuntimeConfigurationVersion {
		return fmt.Errorf("BOOTSTRAP_RUNTIME_CONFIGURATION_VERSION must be %q", RuntimeConfigurationVersion)
	}
	return nil
}

// NewCommandDelivery reuses trusted worker clients and existing metadata/storage.
func NewCommandDelivery(config aws.Config, records CommandRecords, bucket, project, environment, scriptKey, digestHex string) (app.HostCommandDelivery, error) {
	if err := ValidateRuntimeConfiguration(); err != nil {
		return app.HostCommandDelivery{}, err
	}
	digest, err := hex.DecodeString(digestHex)
	if err != nil || len(digest) != 32 || records == nil || bucket == "" || project == "" || environment == "" || scriptKey == "" {
		return app.HostCommandDelivery{}, fmt.Errorf("host command delivery configuration invalid")
	}
	config.ClientLogMode = 0
	objects := s3.NewFromConfig(config)
	return app.HostCommandDelivery{Issuer: app.HostManifestIssuer{
		Inputs:   app.HostObjectIssuer{Authority: app.HostAccessAuthority{Records: records, Instances: hostaccess.InstanceAuthority{Client: ec2.NewFromConfig(config), Project: project, Environment: environment}}, Signer: hostaccess.NewSigner(config, bucket), Script: domain.HostObjectRequest{Purpose: domain.HostObjectBootstrapScript, Slot: "bootstrap-script", Key: scriptKey, SHA256: base64.StdEncoding.EncodeToString(digest), MaxBytes: domain.MaxHostAccessManifestBytes}},
		Attempts: records, Exchange: hostaccess.NewManifestExchange(config, bucket),
	}, Inspector: hostaccess.ReadInspector{Client: objects, Bucket: bucket}, ArchiveInspector: hostaccess.ArchiveInspector{Client: objects, Bucket: bucket}, WorkshopStore: hostaccess.WorkshopStore{Client: objects, Bucket: bucket}}, nil
}
