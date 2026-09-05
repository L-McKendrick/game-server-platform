package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/dynamodbstore"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/ec2orphans"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/resourceinventory"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/s3objects"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/sfnworkflow"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/sqsdlq"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/ssmbootstrap"
	apporphan "github.com/L-McKendrick/game-server-platform/internal/app/orphan"
	appreliability "github.com/L-McKendrick/game-server-platform/internal/app/reliability"
	appsession "github.com/L-McKendrick/game-server-platform/internal/app/sessions"
	"github.com/L-McKendrick/game-server-platform/internal/app/workshopcontent"
	"github.com/L-McKendrick/game-server-platform/internal/config"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/identity"
	"github.com/L-McKendrick/game-server-platform/internal/logging"
	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

type request struct {
	Action               string                 `json:"action"`
	Queue                domain.DeadLetterQueue `json:"queue,omitempty"`
	RequestedBy          string                 `json:"requested_by,omitempty"`
	CorrelationID        string                 `json:"correlation_id,omitempty"`
	FindingID            string                 `json:"finding_id,omitempty"`
	MaxMessagesPerSecond int32                  `json:"max_messages_per_second,omitempty"`
	Limit                int32                  `json:"limit,omitempty"`
}
type handler struct {
	reliability *appreliability.Service
	orphans     *apporphan.Service
	logger      *slog.Logger
}

func main() {
	handler, err := build(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "reliability worker startup error: %v\n", err)
		os.Exit(1)
	}
	lambda.Start(handler.Handle)
}

func build(ctx context.Context) (*handler, error) {
	base, err := config.Load()
	if err != nil {
		return nil, err
	}
	project, bucket := env("PROJECT_NAME", "game-server-platform"), strings.TrimSpace(base.SessionAssetsBucket)
	if bucket == "" {
		return nil, fmt.Errorf("SESSION_ASSETS_BUCKET is required")
	}
	awsConfig, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(base.AWSRegion))
	if err != nil {
		return nil, err
	}
	dynamodbClient, ec2Client, s3Client, sfnClient, sqsClient := dynamodb.NewFromConfig(awsConfig), ec2.NewFromConfig(awsConfig), s3.NewFromConfig(awsConfig), sfn.NewFromConfig(awsConfig), sqs.NewFromConfig(awsConfig)
	repository := dynamodbstore.New(dynamodbClient, base.MetadataTable)
	ids, clock := identity.Generator{}, appsession.SystemClock{}
	dlq, err := sqsdlq.New(sqsClient, map[domain.DeadLetterQueue]sqsdlq.Endpoint{
		domain.DeadLetterCommands: endpoint("COMMAND"), domain.DeadLetterNotifications: endpoint("NOTIFICATION"), domain.DeadLetterArtifacts: endpoint("ARTIFACT"),
	})
	if err != nil {
		return nil, err
	}
	reliability, err := appreliability.NewService(repository, repository, repository, ids, clock)
	if err != nil {
		return nil, err
	}
	reliability.WithExecutionInspector(sfnworkflow.NewInspector(sfnClient)).WithDeadLetterManager(dlq)
	timeoutSeconds := int32(21600)
	contentRunner, err := ssmbootstrap.New(ssm.NewFromConfig(awsConfig), ssmbootstrap.Config{Region: base.AWSRegion, AssetsBucket: bucket, BootstrapScriptKey: strings.TrimSpace(os.Getenv("BOOTSTRAP_SCRIPT_KEY")), MetadataTableName: base.MetadataTable, SteamAuthSecretID: strings.TrimSpace(os.Getenv("STEAM_AUTH_SECRET_ID")), TeamSpeakVersion: env("TEAMSPEAK_VERSION", "3.13.8"), TimeoutSeconds: timeoutSeconds})
	if err != nil {
		return nil, err
	}
	contentSync, err := workshopcontent.New(repository, repository, contentRunner, ids, clock, workshopcontent.WithWorkshopMissionManifest(s3objects.New(s3Client, bucket)))
	if err != nil {
		return nil, err
	}
	reliability.WithActiveWorkflowReconciler(contentSync)
	inventory, err := resourceinventory.New(ec2Client, s3Client, project, base.Environment, bucket)
	if err != nil {
		return nil, err
	}
	cleaner, err := ec2orphans.New(ec2Client, project, base.Environment)
	if err != nil {
		return nil, err
	}
	minimumAge, err := hours("ORPHAN_MINIMUM_AGE_HOURS", 24)
	if err != nil {
		return nil, err
	}
	quarantine, err := hours("ORPHAN_QUARANTINE_HOURS", 24)
	if err != nil {
		return nil, err
	}
	orphans, err := apporphan.NewService(repository, inventory, cleaner, clock, apporphan.Config{Project: project, Environment: base.Environment, MinimumAge: minimumAge, QuarantinePeriod: quarantine, SessionLimit: 1000})
	if err != nil {
		return nil, err
	}
	return &handler{reliability: reliability, orphans: orphans, logger: logging.New(base.LogLevel)}, nil
}

func (handler *handler) Handle(ctx context.Context, input request) (any, error) {
	action := strings.TrimSpace(input.Action)
	if action == "" {
		action = "scheduled"
	}
	limit := input.Limit
	if limit < 1 {
		limit = 100
	}
	switch action {
	case "scheduled":
		workflows, err := handler.reliability.ReconcileWorkflows(ctx, limit)
		if err != nil {
			return nil, err
		}
		orphans, err := handler.orphans.Scan(ctx)
		if err != nil {
			return nil, err
		}
		handler.logger.Info("scheduled reliability scan complete", slog.Int("workflow_findings", workflows.Findings), slog.Int("orphan_findings", orphans.Findings))
		return map[string]any{"workflows": workflows, "orphans": orphans}, nil
	case "reconcile_workflows":
		return handler.reliability.ReconcileWorkflows(ctx, limit)
	case "scan_orphans":
		return handler.orphans.Scan(ctx)
	case "inspect_orphans":
		return handler.orphans.Inspect(ctx, limit)
	case "quarantine_orphan":
		return handler.orphans.Quarantine(ctx, input.FindingID, input.RequestedBy)
	case "cleanup_orphan":
		return handler.orphans.Cleanup(ctx, input.FindingID, input.RequestedBy)
	case "inspect_dlq":
		return handler.reliability.InspectDeadLetter(ctx, input.Queue, input.RequestedBy, input.CorrelationID)
	case "redrive_dlq":
		return handler.reliability.RedriveDeadLetter(ctx, input.Queue, input.RequestedBy, input.CorrelationID, input.MaxMessagesPerSecond)
	default:
		return nil, fmt.Errorf("unsupported reliability action %q", action)
	}
}

func endpoint(prefix string) sqsdlq.Endpoint {
	return sqsdlq.Endpoint{DLQURL: strings.TrimSpace(os.Getenv(prefix + "_DLQ_URL")), DLQARN: strings.TrimSpace(os.Getenv(prefix + "_DLQ_ARN")), DestinationARN: strings.TrimSpace(os.Getenv(prefix + "_QUEUE_ARN"))}
}
func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
func hours(name string, fallback int) (time.Duration, error) {
	value := env(name, strconv.Itoa(fallback))
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 {
		return 0, fmt.Errorf("%s must be positive hours", name)
	}
	return time.Duration(parsed) * time.Hour, nil
}
