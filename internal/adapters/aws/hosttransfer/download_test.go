package hosttransfer

import (
	"os/exec"
	"runtime"
	"testing"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

func TestDownloaderFailuresPreserveAcceptedFilesAndGeneration(t *testing.T) {
	python := "python3"
	if runtime.GOOS == "windows" {
		python = "python"
	}
	command := exec.Command(python, "-B", "-m", "unittest", "download_test.py")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("host downloader regressions: %v\n%s", err, output)
	}
	if len(InstallShell())+domain.MaxHostAccessReferenceBytes*4/3 > domain.MaxHostAccessCommandBytes {
		t.Fatal("helper plus largest encoded reference exceeds command budget")
	}
}
