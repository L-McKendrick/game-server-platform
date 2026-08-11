package main

import (
	"archive/zip"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func main() {
	source := flag.String("source", "", "path to the Linux bootstrap binary")
	output := flag.String("output", "", "path to the Lambda zip")
	flag.Parse()
	if err := packageLambda(*source, *output); err != nil {
		fmt.Fprintf(os.Stderr, "package Lambda: %v\n", err)
		os.Exit(1)
	}
}

func packageLambda(source string, output string) error {
	if source == "" || output == "" {
		return fmt.Errorf("-source and -output are required")
	}
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open bootstrap: %w", err)
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	archive, err := os.Create(output)
	if err != nil {
		return fmt.Errorf("create archive: %w", err)
	}
	success := false
	defer func() {
		_ = archive.Close()
		if !success {
			_ = os.Remove(output)
		}
	}()

	writer := zip.NewWriter(archive)
	header := &zip.FileHeader{Name: "bootstrap", Method: zip.Deflate}
	header.SetMode(0o755)
	entry, err := writer.CreateHeader(header)
	if err != nil {
		return fmt.Errorf("create bootstrap entry: %w", err)
	}
	if _, err := io.Copy(entry, input); err != nil {
		return fmt.Errorf("write bootstrap entry: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("close zip writer: %w", err)
	}
	if err := archive.Close(); err != nil {
		return fmt.Errorf("close archive: %w", err)
	}
	success = true
	return nil
}
