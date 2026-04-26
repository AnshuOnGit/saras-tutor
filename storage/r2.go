// Package storage provides object-storage clients used by saras-tutor.
//
// The StorageService in this file uploads images to Cloudflare R2 using the
// S3-compatible endpoint exposed by Cloudflare. It is intended to replace the
// existing DB-backed image blobs for scenarios where images should be served
// via a CDN-friendly public URL instead of being streamed from Postgres.
package storage

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// StorageService uploads images to a Cloudflare R2 bucket over the S3 API.
type StorageService struct {
	client     *s3.Client
	bucket     string
	publicBase string // e.g. https://images.example.com or https://pub-<hash>.r2.dev
}

// R2Config bundles the Cloudflare R2 parameters the service needs.
// All fields are optional in NewStorageService — missing values fall back to
// the documented environment variables.
type R2Config struct {
	AccessKeyID     string // R2 Access Key ID (env: R2_ACCESS_KEY_ID)
	SecretAccessKey string // R2 Secret Access Key (env: R2_SECRET_ACCESS_KEY)
	Endpoint        string // https://<account>.r2.cloudflarestorage.com (env: R2_ENDPOINT)
	Bucket          string // Target bucket name (env: R2_BUCKET)
	PublicBaseURL   string // Public base URL for built objects (env: R2_PUBLIC_BASE_URL)
}

// NewStorageService constructs an R2-backed StorageService.
//
// If cfg contains zero values, the corresponding environment variables are
// consulted:
//
//	R2_ACCESS_KEY_ID
//	R2_SECRET_ACCESS_KEY
//	R2_ENDPOINT              // https://<account>.r2.cloudflarestorage.com
//	R2_BUCKET
//	R2_PUBLIC_BASE_URL       // https://<hash>.r2.dev OR your custom CDN domain
//
// The region is hard-coded to "auto" as required by R2.
func NewStorageService(ctx context.Context, cfg R2Config) (*StorageService, error) {
	if cfg.AccessKeyID == "" {
		cfg.AccessKeyID = os.Getenv("R2_ACCESS_KEY_ID")
	}
	if cfg.SecretAccessKey == "" {
		cfg.SecretAccessKey = os.Getenv("R2_SECRET_ACCESS_KEY")
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = os.Getenv("R2_ENDPOINT")
	}
	if cfg.Bucket == "" {
		cfg.Bucket = os.Getenv("R2_BUCKET")
	}
	if cfg.PublicBaseURL == "" {
		cfg.PublicBaseURL = os.Getenv("R2_PUBLIC_BASE_URL")
	}

	if cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		return nil, fmt.Errorf("storage: R2 credentials missing (R2_ACCESS_KEY_ID / R2_SECRET_ACCESS_KEY)")
	}
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("storage: R2_ENDPOINT is required")
	}
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("storage: R2_BUCKET is required")
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("auto"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("storage: load aws config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(cfg.Endpoint)
		// R2 requires path-style addressing for the S3 API.
		o.UsePathStyle = true
	})

	return &StorageService{
		client:     client,
		bucket:     cfg.Bucket,
		publicBase: strings.TrimRight(cfg.PublicBaseURL, "/"),
	}, nil
}

// UploadImage uploads the given image bytes to R2 under a deterministic
// object key derived from fileName and returns the public URL of the object.
//
// Content-Type is inferred from the file extension when recognisable,
// otherwise net/http's DetectContentType is used as a fallback. Only image
// MIME types are allowed — anything else returns an error.
func (s *StorageService) UploadImage(ctx context.Context, imageData []byte, fileName string) (string, error) {
	if len(imageData) == 0 {
		return "", fmt.Errorf("storage: empty image data")
	}
	if fileName == "" {
		return "", fmt.Errorf("storage: fileName is required")
	}

	contentType, err := resolveImageContentType(fileName, imageData)
	if err != nil {
		return "", err
	}

	key := buildObjectKey(fileName)

	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(imageData),
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return "", fmt.Errorf("storage: put object %s: %w", key, err)
	}

	return s.publicURL(key), nil
}

// publicURL composes the externally reachable URL for an uploaded key.
func (s *StorageService) publicURL(key string) string {
	if s.publicBase != "" {
		return s.publicBase + "/" + key
	}
	// Best-effort fallback: most users configure a public base, but if not we
	// still return something usable behind the S3 endpoint.
	endpoint := ""
	if ep := s.client.Options().BaseEndpoint; ep != nil {
		endpoint = strings.TrimRight(*ep, "/")
	}
	return fmt.Sprintf("%s/%s/%s", endpoint, s.bucket, key)
}

// buildObjectKey namespaces uploads by date and adds a unix-nanos prefix to
// avoid collisions on same-name uploads.
func buildObjectKey(fileName string) string {
	base := path.Base(fileName)
	date := time.Now().UTC().Format("2006/01/02")
	return fmt.Sprintf("images/%s/%d-%s", date, time.Now().UnixNano(), base)
}

// resolveImageContentType picks image/png, image/jpeg, image/webp, or
// image/gif based on the filename extension, falling back to sniffing the
// leading bytes. Returns an error for non-image content.
func resolveImageContentType(fileName string, data []byte) (string, error) {
	switch strings.ToLower(path.Ext(fileName)) {
	case ".png":
		return "image/png", nil
	case ".jpg", ".jpeg":
		return "image/jpeg", nil
	case ".webp":
		return "image/webp", nil
	case ".gif":
		return "image/gif", nil
	}

	// Fallback: sniff the first 512 bytes.
	sniffLen := 512
	if len(data) < sniffLen {
		sniffLen = len(data)
	}
	detected := http.DetectContentType(data[:sniffLen])
	if !strings.HasPrefix(detected, "image/") {
		return "", fmt.Errorf("storage: unsupported content type %q for %s", detected, fileName)
	}
	return detected, nil
}
