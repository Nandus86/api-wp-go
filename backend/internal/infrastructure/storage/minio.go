package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Storage is an S3-compatible object store (MinIO). A nil *Storage means storage is disabled;
// every method is nil-safe and reports ErrDisabled.
type Storage struct {
	client *minio.Client
	// signer presigns URLs against the public endpoint. SigV4 signs the Host header,
	// so rewriting the host of an internally signed URL would invalidate it.
	signer *minio.Client
	bucket string
}

var ErrDisabled = fmt.Errorf("object storage is not configured")

type Options struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	UseSSL    bool
	Bucket    string
	// PublicURL, when set, replaces scheme+host of presigned URLs so links handed to
	// external consumers point to a reachable address instead of the internal docker host.
	PublicURL string
}

func New(ctx context.Context, opts Options) (*Storage, error) {
	if opts.Endpoint == "" {
		return nil, nil
	}
	client, err := minio.New(opts.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(opts.AccessKey, opts.SecretKey, ""),
		Secure: opts.UseSSL,
		Region: "us-east-1",
	})
	if err != nil {
		return nil, fmt.Errorf("minio client: %w", err)
	}
	s := &Storage{client: client, signer: client, bucket: opts.Bucket}
	if opts.PublicURL != "" {
		pub, err := url.Parse(opts.PublicURL)
		if err != nil || pub.Host == "" {
			return nil, fmt.Errorf("invalid MINIO_PUBLIC_URL %q", opts.PublicURL)
		}
		// Region is fixed so presigning never needs a network round-trip to the public host.
		signer, err := minio.New(pub.Host, &minio.Options{
			Creds:  credentials.NewStaticV4(opts.AccessKey, opts.SecretKey, ""),
			Secure: pub.Scheme == "https",
			Region: "us-east-1",
		})
		if err != nil {
			return nil, fmt.Errorf("minio public signer: %w", err)
		}
		s.signer = signer
	}

	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	exists, err := client.BucketExists(cctx, opts.Bucket)
	if err != nil {
		return nil, fmt.Errorf("minio bucket check: %w", err)
	}
	if !exists {
		if err := client.MakeBucket(cctx, opts.Bucket, minio.MakeBucketOptions{}); err != nil {
			return nil, fmt.Errorf("minio create bucket: %w", err)
		}
	}
	return s, nil
}

func (s *Storage) Enabled() bool { return s != nil && s.client != nil }

func (s *Storage) Put(ctx context.Context, key string, data []byte, contentType string) error {
	if !s.Enabled() {
		return ErrDisabled
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	_, err := s.client.PutObject(ctx, s.bucket, key, bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{ContentType: contentType})
	return err
}

func (s *Storage) Get(ctx context.Context, key string) ([]byte, string, error) {
	if !s.Enabled() {
		return nil, "", ErrDisabled
	}
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, "", err
	}
	defer obj.Close()
	info, err := obj.Stat()
	if err != nil {
		return nil, "", err
	}
	data, err := io.ReadAll(obj)
	return data, info.ContentType, err
}

func (s *Storage) Delete(ctx context.Context, key string) error {
	if !s.Enabled() {
		return ErrDisabled
	}
	return s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
}

// PresignedURL returns a temporary GET link for the object.
func (s *Storage) PresignedURL(ctx context.Context, key string, expiry time.Duration) (string, error) {
	if !s.Enabled() {
		return "", ErrDisabled
	}
	u, err := s.signer.PresignedGetObject(ctx, s.bucket, key, expiry, url.Values{})
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

func (s *Storage) Ping(ctx context.Context) error {
	if !s.Enabled() {
		return ErrDisabled
	}
	_, err := s.client.BucketExists(ctx, s.bucket)
	return err
}
