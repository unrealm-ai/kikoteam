package skillasset

import (
	"context"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"os"
	"testing"
)

// Optional real S3-compatible integration, deliberately isolated from production configuration.
func TestS3CompatibleImmutableRoundTrip(t *testing.T) {
	endpoint := os.Getenv("SKILL_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("SKILL_TEST_S3_ENDPOINT required for real S3 integration")
	}
	ctx := context.Background()
	s, e := NewStore(ctx, Config{S3Configured: true, Endpoint: endpoint, Region: "us-east-1", Bucket: os.Getenv("SKILL_TEST_S3_BUCKET"), AccessKeyID: os.Getenv("SKILL_TEST_S3_ACCESS_KEY"), SecretAccessKey: os.Getenv("SKILL_TEST_S3_SECRET_KEY"), ForcePathStyle: true, Prefix: "skill-tests/" + service.NewID()})
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.Prepare(ctx, service.SkillImportRequest{Source: "markdown", Markdown: goodMD})
	if e != nil {
		t.Fatal(e)
	}
	tmp := "temp/uploads/skill-imports/ws/receipt"
	dest := "workspaces/ws/skills/s/versions/v"
	if e = s.Write(ctx, tmp, b); e != nil {
		t.Fatal(e)
	}
	if e = s.Write(ctx, tmp, b); e != nil {
		t.Fatalf("idempotence: %v", e)
	}
	if e = s.put(ctx, tmp+"/files/SKILL.md", []byte("different")); e == nil {
		t.Fatal("immutable overwrite accepted")
	}
	if e = s.Copy(ctx, tmp, dest, b.Manifest); e != nil {
		t.Fatal(e)
	}
	if e = s.Cleanup(ctx, tmp, b.Manifest); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Read(ctx, tmp, b.Manifest.Files[0]); e == nil {
		t.Fatal("staging not cleaned")
	}
	data, e := s.Read(ctx, dest, b.Manifest.Files[0])
	if e != nil || string(data) != goodMD {
		t.Fatalf("published changed: %v", e)
	}
	if e = s.Cleanup(ctx, dest, b.Manifest); e == nil {
		t.Fatal("cleanup accepted published prefix")
	}
}
