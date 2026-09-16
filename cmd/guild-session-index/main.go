package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/dynamodbstore"
)

func main() {
	mode := flag.String("mode", "verify", "operation: backfill, verify, or cutover")
	table := flag.String("table", strings.TrimSpace(os.Getenv("METADATA_TABLE_NAME")), "DynamoDB metadata table")
	region := flag.String("region", env("AWS_REGION", "us-west-2"), "AWS region")
	pageSize := flag.Int("page-size", 100, "backfill scan page size (1-1000)")
	flag.Parse()
	if strings.TrimSpace(*table) == "" {
		fatalf("-table or METADATA_TABLE_NAME is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	configuration, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(*region))
	if err != nil {
		fatalf("load AWS configuration: %v", err)
	}
	repository := dynamodbstore.New(dynamodb.NewFromConfig(configuration), *table)
	switch strings.ToLower(strings.TrimSpace(*mode)) {
	case "backfill":
		cursor, updated, conflicts := "", 0, 0
		for {
			result, err := repository.BackfillGuildSessionIndexPage(ctx, cursor, int32(*pageSize))
			if err != nil {
				fatalf("backfill failed: %v", err)
			}
			updated += result.Updated
			conflicts += result.Conflicted
			fmt.Printf("scanned=%d updated=%d conflicts=%d continuation=%t\n", result.Scanned, result.Updated, result.Conflicted, result.NextCursor != "")
			cursor = result.NextCursor
			if cursor == "" {
				break
			}
		}
		fmt.Printf("backfill complete: updated=%d conflicts=%d\n", updated, conflicts)
		if conflicts != 0 {
			fatalf("concurrent changes were detected; rerun backfill before verification")
		}
	case "verify":
		verification, err := repository.VerifyGuildSessionIndex(ctx)
		printVerification(verification)
		if err != nil {
			fatalf("verification failed: %v", err)
		}
		if !verification.Valid() {
			fatalf("verification did not match; indexed reads remain disabled")
		}
	case "cutover":
		verification, err := repository.EnableGuildSessionIndex(ctx, time.Now())
		printVerification(verification)
		if err != nil {
			fatalf("cutover refused: %v", err)
		}
		fmt.Println("guild-session indexed reads enabled")
	default:
		fatalf("unsupported mode %q", *mode)
	}
}

func printVerification(verification dynamodbstore.GuildSessionIndexVerification) {
	fmt.Printf("source=%d indexed=%d missing_or_invalid_keys=%d mismatched_ranges=%d\n", verification.SourceCount, verification.IndexedCount, verification.MissingKeys, len(verification.Mismatched))
	keys := make([]string, 0, len(verification.Mismatched))
	for key := range verification.Mismatched {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		counts := verification.Mismatched[key]
		fmt.Printf("mismatch=%s source=%d indexed=%d\n", key, counts[0], counts[1])
	}
}

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func fatalf(format string, values ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", values...)
	os.Exit(1)
}
