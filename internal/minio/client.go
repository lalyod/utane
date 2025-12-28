package minio

import (
	"context"
	"log"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type Client struct {
	*minio.Client
	Bucket string
}

func New(endpoint, accessKey,secretKey, bucket string, useSSL bool) *Client {
	client, err := minio.New(endpoint, &minio.Options{
		Creds: credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		log.Fatal(err)
	}

	return &Client{
		Client: client,
		Bucket:  bucket,
	}
}

func (c *Client) EnsureBucket(ctx context.Context){
	exists, err := c.BucketExists(ctx, c.Bucket)
	if err != nil {
		log.Fatal(err)
	}

	if !exists {
		err = c.MakeBucket(ctx, c.Bucket, minio.MakeBucketOptions{})
		if err != nil {
			log.Fatal(err)
		}
	}
}
