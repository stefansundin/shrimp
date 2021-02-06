package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/stefansundin/go-flowrate/flowrate"
	"golang.org/x/sys/unix"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3Types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// Each part must be at least 5 MiB in size (except the last part).
var chunksize int64 = 5 * 1024 * 1024

func init() {
	// Do not fail if a region is not specified anywhere
	// This is only used for the first call that looks up the bucket region
	if _, present := os.LookupEnv("AWS_DEFAULT_REGION"); !present {
		os.Setenv("AWS_DEFAULT_REGION", "us-east-1")
	}
}

func main() {
	// TODO: Make the flags consistent with the aws cli
	var profile, bucket, key, file, bwlimit string
	var version bool
	flag.StringVar(&profile, "profile", "default", "The profile to use.")
	flag.StringVar(&bucket, "bucket", "", "Bucket name.")
	flag.StringVar(&key, "key", "", "Destination object key name.")
	flag.StringVar(&file, "file", "", "Input file.")
	flag.StringVar(&bwlimit, "bwlimit", "", "Bandwidth limit (e.g. \"2.5m\").")
	flag.BoolVar(&version, "version", false, "Print version number.")
	flag.Parse()

	if version {
		fmt.Println("0.0.1")
		os.Exit(0)
	}

	if bucket == "" || key == "" || file == "" {
		fmt.Println("--bucket, --key, and --file are all required!")
		os.Exit(1)
	}

	var rate int64
	if bwlimit != "" {
		var err error
		rate, err = parseLimit(bwlimit)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	// Check if we can read from the file
	if err := unix.Access(file, unix.R_OK); err != nil {
		fmt.Println("Error: can not read from the file.")
		os.Exit(1)
	}

	// Get the file size
	// TODO: Check if the file has been modified since the multi part was started and print a warning
	stat, err := os.Stat(file)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fileSize := stat.Size()
	fmt.Printf("File size: %d bytes\n", fileSize)

	// Open the file
	f, _ := os.Open(file)
	defer f.Close()

	// Initialize the AWS SDK
	cfg, err := config.LoadDefaultConfig(
		context.TODO(),
		config.WithSharedConfigProfile(profile),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	client := s3.NewFromConfig(cfg)

	// Get the bucket location
	bucketLocationOutput, err := client.GetBucketLocation(context.TODO(), &s3.GetBucketLocationInput{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	bucketRegion := bucketLocationOutput.LocationConstraint
	if bucketRegion == "" {
		// This can be updated when aws-sdk-go-v2 supports GetBucketLocation WithNormalizeBucketLocation
		bucketRegion = "us-east-1"
	}
	client = s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.Region = string(bucketRegion)
	})

	// Abort if the object already exists
	_, err = client.HeadObject(context.TODO(), &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err == nil {
		fmt.Println("The object already exists in the S3 bucket. Please delete it first.")
		os.Exit(1)
	}

	// Check if we should resume an upload
	fmt.Println("Checking if this upload is already in progress...")
	var uploadId string
	// TODO: Switch this to a paginator when aws-sdk-go-v2 supports it?
	outputListMultipartUploads, err := client.ListMultipartUploads(context.TODO(), &s3.ListMultipartUploadsInput{
		Bucket: aws.String(bucket),
		Prefix: aws.String(key),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, upload := range outputListMultipartUploads.Uploads {
		if *upload.Key != key {
			continue
		}

		fmt.Printf("Upload: %+v\n", upload)
		if uploadId != "" {
			fmt.Println("Error: more than one previous upload is in progress. Please abort duplicated in-progress uploads manually.")
			os.Exit(1)
		}
		uploadId = *upload.UploadId
	}

	// Create the multipart upload or get the part information from an existing upload
	parts := []s3Types.CompletedPart{}
	var partNumber int32
	var offset int64
	if uploadId == "" {
		fmt.Println("Creating multipart upload.")
		outputCreateMultipartUpload, err := client.CreateMultipartUpload(context.TODO(), &s3.CreateMultipartUploadInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		uploadId = *outputCreateMultipartUpload.UploadId
		fmt.Printf("Upload id: %v\n", uploadId)
	} else {
		fmt.Printf("Found an existing upload in progress with upload id: %v\n", uploadId)

		var lastModified time.Time
		paginatorListParts := s3.NewListPartsPaginator(client, &s3.ListPartsInput{
			Bucket:   aws.String(bucket),
			Key:      aws.String(key),
			UploadId: aws.String(uploadId),
		})
		for paginatorListParts.HasMorePages() {
			page, err := paginatorListParts.NextPage(context.TODO())
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			partNumber += int32(len(page.Parts))
			for _, part := range page.Parts {
				fmt.Printf("Part: %+v\n", part)
				if (*part.LastModified).After(lastModified) {
					lastModified = *part.LastModified
				}
				offset += part.Size
				parts = append(parts, s3Types.CompletedPart{
					PartNumber: part.PartNumber,
					ETag:       part.ETag,
				})
			}
			// https://github.com/aws/aws-sdk-go-v2/pull/1100
			if !page.IsTruncated {
				break
			}
		}

		localLocation, err := time.LoadLocation("Local")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("Continuing upload from %v. %d parts already uploaded (%d bytes).\n", lastModified.In(localLocation), len(parts), offset)

		if offset > fileSize {
			fmt.Println("Error: size of parts already uploaded is greater than local file size.")
			os.Exit(1)
		}
	}

	// Trap Ctrl-C signal
	interrupted := false
	signalChannel := make(chan os.Signal, 1)
	signal.Notify(signalChannel, os.Interrupt)
	go func() {
		for sig := range signalChannel {
			if sig != os.Interrupt {
				continue
			}
			if interrupted {
				os.Exit(1)
			}
			interrupted = true
			fmt.Println("\nInterrupt received, finishing current part. Press Ctrl-C again to exit immediately.")
		}
	}()

	needMoreParts := (offset < fileSize)
	for needMoreParts {
		partNumber += 1

		var partSize int64
		if fileSize-offset > chunksize {
			partSize = chunksize
		} else {
			partSize = fileSize - offset
		}

		partData := make([]byte, partSize)
		n, err := f.ReadAt(partData, offset)
		if err != nil && err != io.EOF {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		var reader io.Reader
		if rate == 0 {
			reader = bytes.NewReader(partData)
		} else {
			reader = flowrate.NewReader(bytes.NewReader(partData), rate)
		}

		fmt.Printf("Uploading part %d (%d bytes)..", partNumber, len(partData))
		partStartTime := time.Now()
		outputUploadPart, err := client.UploadPart(context.TODO(), &s3.UploadPartInput{
			Bucket:     aws.String(bucket),
			Key:        aws.String(key),
			UploadId:   aws.String(uploadId),
			PartNumber: partNumber,
			Body:       reader,
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("\rUploaded part %d (%d bytes) in %s.\n", partNumber, len(partData), time.Since(partStartTime).Round(time.Second))

		// Check if the user wants to stop
		if interrupted {
			fmt.Println("Exited early.")
			os.Exit(0)
		}

		parts = append(parts, s3Types.CompletedPart{
			ETag:       outputUploadPart.ETag,
			PartNumber: partNumber,
		})
		offset += int64(n)
		needMoreParts = (offset < fileSize)
	}
	signal.Reset(os.Interrupt)

	// Do a sanity check
	if offset != fileSize {
		fmt.Println("Something went terribly wrong (offset != fileSize).")
		os.Exit(1)
	}

	// Complete the upload
	_, err = client.CompleteMultipartUpload(context.TODO(), &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(bucket),
		Key:      aws.String(key),
		UploadId: aws.String(uploadId),
		MultipartUpload: &s3Types.CompletedMultipartUpload{
			Parts: parts,
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	fmt.Println("All done!")
}
