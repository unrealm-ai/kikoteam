package skillasset

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type Config struct {
	LocalRoot, Endpoint, Bucket, Region, AccessKeyID, SecretAccessKey, SessionToken, Prefix string
	ForcePathStyle                                                                          bool
	S3Configured                                                                            bool
}

func LoadConfig(getenv func(string) string) (Config, error) {
	c := Config{LocalRoot: getenv("KIKO_STORAGE_LOCAL_ROOT"), Endpoint: getenv("KIKO_STORAGE_S3_ENDPOINT"), Bucket: getenv("KIKO_STORAGE_S3_BUCKET"), Region: getenv("KIKO_STORAGE_S3_REGION"), AccessKeyID: getenv("KIKO_STORAGE_S3_ACCESS_KEY_ID"), SecretAccessKey: getenv("KIKO_STORAGE_S3_SECRET_ACCESS_KEY"), SessionToken: getenv("KIKO_STORAGE_S3_SESSION_TOKEN"), Prefix: getenv("KIKO_STORAGE_S3_PREFIX")}
	raw := getenv("KIKO_STORAGE_S3_FORCE_PATH_STYLE")
	var e error
	if raw != "" {
		c.ForcePathStyle, e = strconv.ParseBool(raw)
		if e != nil {
			return c, fmt.Errorf("KIKO_STORAGE_S3_FORCE_PATH_STYLE must be boolean")
		}
	}
	c.S3Configured = c.Endpoint != "" || c.Bucket != "" || c.Region != "" || c.AccessKeyID != "" || c.SecretAccessKey != "" || c.SessionToken != "" || c.Prefix != "" || raw != ""
	if c.LocalRoot == "" {
		c.LocalRoot = "~/.kiko"
	}
	if c.LocalRoot == "~" || strings.HasPrefix(c.LocalRoot, "~/") {
		home, e := os.UserHomeDir()
		if e != nil {
			return c, e
		}
		c.LocalRoot = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(c.LocalRoot, "~"), "/"))
	}
	c.LocalRoot, e = filepath.Abs(c.LocalRoot)
	if e != nil || !c.S3Configured {
		return c, e
	}
	if c.Bucket == "" || c.Region == "" || (c.AccessKeyID == "") != (c.SecretAccessKey == "") || c.SessionToken != "" && c.AccessKeyID == "" {
		return c, fmt.Errorf("S3 needs bucket, region and a complete credential pair (or SDK credential chain)")
	}
	c.Prefix = strings.Trim(c.Prefix, "/")
	if c.Prefix != "" && !validPath(c.Prefix) {
		return c, fmt.Errorf("invalid S3 prefix")
	}
	if c.Endpoint != "" {
		u, e := url.Parse(c.Endpoint)
		if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" {
			return c, fmt.Errorf("S3 endpoint must be a HTTPS origin")
		}
	}
	return c, nil
}

type s3Client interface {
	DeleteObject(context.Context, *s3.DeleteObjectInput, ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}
type Store struct {
	root                     *os.Root
	client                   s3Client
	bucket, prefix, identity string
	githubClient             *http.Client
}

func NewStore(ctx context.Context, c Config) (*Store, error) {
	s := &Store{bucket: c.Bucket, prefix: c.Prefix, githubClient: githubHTTPClient()}
	if !c.S3Configured {
		if e := os.MkdirAll(c.LocalRoot, 0700); e != nil {
			return nil, e
		}
		r, e := os.OpenRoot(c.LocalRoot)
		if e != nil {
			return nil, e
		}
		s.root = r
		s.identity = "local:" + hash([]byte(c.LocalRoot))
		return s, nil
	}
	opts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(c.Region), awsconfig.WithRetryMaxAttempts(2), awsconfig.WithHTTPClient(&http.Client{Timeout: 30 * time.Second})}
	if c.AccessKeyID != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(c.AccessKeyID, c.SecretAccessKey, c.SessionToken)))
	}
	cfg, e := awsconfig.LoadDefaultConfig(ctx, opts...)
	if e != nil {
		return nil, fmt.Errorf("load S3 configuration: %w", e)
	}
	s.client = s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = c.ForcePathStyle
		if c.Endpoint != "" {
			o.BaseEndpoint = aws.String(c.Endpoint)
		}
	})
	s.identity = "s3:" + hash([]byte(c.Endpoint+"\n"+c.Region+"\n"+c.Bucket+"\n"+c.Prefix))
	return s, nil
}
func (s *Store) Close() error {
	if s.root != nil {
		return s.root.Close()
	}
	return nil
}
func (s *Store) Identity() string { return s.identity }
func (s *Store) key(key string) (string, error) {
	if !validPath(key) {
		return "", fmt.Errorf("invalid internal asset key")
	}
	if s.prefix != "" {
		return s.prefix + "/" + key, nil
	}
	return key, nil
}
func (s *Store) put(ctx context.Context, key string, b []byte) error {
	k, e := s.key(key)
	if e != nil {
		return e
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	if s.root != nil {
		return s.localPut(ctx, key, b)
	}
	_, e = s.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(k), Body: bytes.NewReader(b), ContentLength: aws.Int64(int64(len(b))), IfNoneMatch: aws.String("*")})
	var api smithy.APIError
	if e != nil && errors.As(e, &api) && (api.ErrorCode() == "PreconditionFailed" || api.ErrorCode() == "ConditionalRequestConflict") {
		existing, re := s.get(ctx, key, int64(len(b)))
		if re == nil && bytes.Equal(existing, b) {
			return nil
		}
	}
	return e
}
func (s *Store) localPut(ctx context.Context, key string, b []byte) error {
	if e := s.root.MkdirAll(path.Dir(key), 0700); e != nil {
		return e
	}
	// Callers publish the database reference after the file is complete.
	f, e := s.root.OpenFile(key, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(e, os.ErrExist) {
		existing, readErr := s.get(ctx, key, int64(len(b)))
		if readErr == nil && bytes.Equal(existing, b) {
			return nil
		}
		return errors.Join(e, readErr)
	}
	if e != nil {
		return e
	}
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	return errors.Join(e, f.Close())
}
func (s *Store) get(ctx context.Context, key string, limit int64) ([]byte, error) {
	k, e := s.key(key)
	if e != nil {
		return nil, e
	}
	if e = ctx.Err(); e != nil {
		return nil, e
	}
	var r io.ReadCloser
	if s.root != nil {
		r, e = s.root.Open(key)
	} else {
		var out *s3.GetObjectOutput
		out, e = s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(k)})
		if e == nil {
			r = out.Body
		}
	}
	if e != nil {
		return nil, e
	}
	defer r.Close() //nolint:errcheck // Read result is checked; close is best effort.
	b, e := io.ReadAll(io.LimitReader(r, limit+1))
	if e == nil && int64(len(b)) > limit {
		return nil, fmt.Errorf("asset size exceeds manifest")
	}
	return b, e
}
func (s *Store) Write(ctx context.Context, prefix string, b service.SkillBundle) error {
	for _, f := range b.Manifest.Files {
		data, ok := b.Files[f.Path]
		if !ok {
			return fmt.Errorf("manifest file missing")
		}
		if e := verifyFile(f, data); e != nil {
			return e
		}
		if e := s.put(ctx, prefix+"/files/"+f.Path, data); e != nil {
			return e
		}
	}
	m, e := json.Marshal(b.Manifest)
	if e != nil {
		return e
	}
	return s.put(ctx, prefix+"/manifest.json", m)
}
func (s *Store) Read(ctx context.Context, prefix string, f service.SkillFile) ([]byte, error) {
	if !validPath(f.Path) || f.Size < 0 || f.Size > MaxFile {
		return nil, fmt.Errorf("invalid asset manifest")
	}
	b, e := s.get(ctx, prefix+"/files/"+f.Path, f.Size)
	if e == nil {
		e = verifyFile(f, b)
	}
	return b, e
}
func (s *Store) Copy(ctx context.Context, from, to string, m service.SkillManifest) error {
	// No directory rename assumption: every object is immutable; DB makes the version visible last.
	for _, f := range m.Files {
		b, e := s.Read(ctx, from, f)
		if e != nil {
			return e
		}
		if e = s.put(ctx, to+"/files/"+f.Path, b); e != nil {
			return e
		}
	}
	b, e := json.Marshal(m)
	if e != nil {
		return e
	}
	return s.put(ctx, to+"/manifest.json", b)
}

// Cleanup only accepts receipt staging prefixes; published versions cannot be deleted here.
func (s *Store) Cleanup(ctx context.Context, prefix string, m service.SkillManifest) error {
	if !strings.HasPrefix(prefix, "temp/uploads/skill-imports/") || !validPath(prefix) || len(strings.Split(prefix, "/")) != 5 {
		return fmt.Errorf("invalid cleanup prefix")
	}
	keys := []string{prefix + "/manifest.json"}
	for _, f := range m.Files {
		if !validPath(f.Path) {
			return fmt.Errorf("invalid manifest path")
		}
		keys = append(keys, prefix+"/files/"+f.Path)
	}
	for _, key := range keys {
		if s.root != nil {
			if e := s.root.Remove(key); e != nil && !errors.Is(e, os.ErrNotExist) {
				return e
			}
		} else {
			k, e := s.key(key)
			if e != nil {
				return e
			}
			if _, e = s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(k)}); e != nil {
				return e
			}
		}
	}
	if s.root != nil {
		return s.root.RemoveAll(prefix)
	}
	return nil
}

// Work files use immutable, workspace-qualified content keys in the configured asset store.
func (s *Store) PutWorkFile(ctx context.Context, workspace string, data []byte) (string, error) {
	digest := hash(data)
	return digest, s.put(ctx, path.Join("workspaces", workspace, "work-files", digest), data)
}
func (s *Store) GetWorkFile(ctx context.Context, workspace, digest string, size int64) ([]byte, error) {
	data, err := s.get(ctx, path.Join("workspaces", workspace, "work-files", digest), size)
	if err == nil && (int64(len(data)) != size || hash(data) != digest) {
		return nil, errors.New("work file integrity check failed")
	}
	return data, err
}
