//go:build ignore

package main

import (
	"context"
	"log"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
)

const (
	endpoint = "http://localhost:9000"
	bucket   = "media"
	user     = "minioadmin"
	password = "minioadmin"
)

var referenced = []string{
	"uploads/img1_low.jpg",
	"uploads/img1_orig.jpg",
	"uploads/img1_prev.jpg",
	"uploads/legacy_img.jpg",
	"uploads/img2_orig.jpg",
	"documents/report.pdf",
	"documents/page1.jpg",
	"documents/page1_low.jpg",
	"documents/page2.jpg",
}
var orphans = []string{
	"uploads/orphan1.jpg",
	"uploads/orphan2.jpg",
	"uploads/orphan3.jpg",
	"documents/old_draft.pdf",
	"documents/tmp_export.pdf",
}

func main() {
	ctx := context.Background()
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(user, password, ""),
		),
	)
	if err != nil {
		log.Fatalf("aws config: %v", err)
	}
	client := awss3.NewFromConfig(awsCfg, func(o *awss3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})

	if _, err := client.CreateBucket(ctx, &awss3.CreateBucketInput{
		Bucket: aws.String(bucket),
	}); err != nil {
		log.Printf("create bucket (may already exist): %v", err)
	}

	log.Printf("Uploading %d referenced + %d orphan objects...",
		len(referenced), len(orphans))
	for _, key := range append(referenced, orphans...) {
		_, err := client.PutObject(ctx, &awss3.PutObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
			Body:   strings.NewReader("seed: " + key),
		})
		if err != nil {
			log.Fatalf("upload %q: %v", key, err)
		}
		log.Printf("good %s", key)
	}
	log.Printf("\nSeed complete.")
	log.Printf("  Referenced objects : %d  (must survive)", len(referenced))
	log.Printf("  Orphan objects     : %d  (must be deleted)", len(orphans))
}
