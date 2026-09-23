package gcs

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestClientRoundTripLive exercises the Cloud Storage client against a real
// bucket. It is opt-in because it writes to live infrastructure:
//
//	OUTPUT_INTEGRATION=1 GCS_OUTPUT_BUCKET=crystal-crm-store go test ./internal/gcs/ -v
//
// The test cleans up after itself, and the prefix keeps its objects out of the
// way of real artifacts.
func TestClientRoundTripLive(t *testing.T) {
	if os.Getenv("OUTPUT_INTEGRATION") != "1" {
		t.Skip("set OUTPUT_INTEGRATION=1 (with credentials) to run against a real bucket")
	}
	bucket := strings.TrimSpace(os.Getenv("GCS_OUTPUT_BUCKET"))
	if bucket == "" {
		bucket = "crystal-crm-store"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client, err := NewClient(ctx, ReadWriteScope)
	if err != nil {
		t.Fatalf("build the client: %v", err)
	}

	object := "WebSearch_GO/.connectivity/" + time.Now().UTC().Format("20060102-150405") + ".txt"
	defer client.DeleteQuietly(context.Background(), bucket, object)

	if _, found, err := client.Download(ctx, bucket, object); err != nil || found {
		t.Fatalf("expected a miss for a fresh object, got found=%v err=%v", found, err)
	}

	payload := []byte("connectivity probe")
	if err := client.Upload(ctx, bucket, object, "text/plain", payload); err != nil {
		t.Fatalf("upload: %v", err)
	}

	got, found, err := client.Download(ctx, bucket, object)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if !found {
		t.Fatal("expected the uploaded object to be readable")
	}
	if string(got) != string(payload) {
		t.Fatalf("round trip returned %q, want %q", got, payload)
	}

	if err := client.Delete(ctx, bucket, object); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, found, _ := client.Download(ctx, bucket, object); found {
		t.Error("expected the object to be gone after deletion")
	}
	t.Logf("live round trip OK in %s", URI(bucket, object))
}
