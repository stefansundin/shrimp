package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"os/signal"
	"sort"
	"time"

	"github.com/stefansundin/shrimp/flowrate"

	"golang.org/x/sys/unix"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3Types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

func init() {
	// Do not fail if a region is not specified anywhere
	// This is only used for the first call that looks up the bucket region
	if _, present := os.LookupEnv("AWS_DEFAULT_REGION"); !present {
		os.Setenv("AWS_DEFAULT_REGION", "us-east-1")
	}
}

func main() {
	// TODO: Make the flags consistent with the aws cli
	var profile, bucket, key, file, bwlimit, contentType, storageClass string
	var version bool
	flag.StringVar(&profile, "profile", "default", "The profile to use.")
	flag.StringVar(&bucket, "bucket", "", "Bucket name.")
	flag.StringVar(&key, "key", "", "Destination object key name.")
	flag.StringVar(&file, "file", "", "Input file.")
	flag.StringVar(&bwlimit, "bwlimit", "", "Bandwidth limit (e.g. \"2.5m\").")
	flag.StringVar(&contentType, "content-type", "", "Content type.")
	flag.StringVar(&storageClass, "storage-class", "", "Storage class (e.g. \"STANDARD\" or \"DEEP_ARCHIVE\").")
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

	createMultipartUploadInput := s3.CreateMultipartUploadInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}

	if contentType != "" {
		createMultipartUploadInput.ContentType = aws.String(contentType)
	}

	if storageClass != "" {
		if v, err := validStorageClass(storageClass); err == nil {
			createMultipartUploadInput.StorageClass = v
		} else {
			fmt.Println(err)
			os.Exit(1)
		}
	}

	var rate int64
	if bwlimit != "" {
		var err error
		rate, err = parseRate(bwlimit)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	initialRate := rate

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
	fmt.Printf("File size: %s\n", formatFilesize(fileSize))

	// Detect best part size
	// Double the part size until the file fits in 10,000 parts.
	// The minimum part size is 5 MiB (except for the last part), although shrimp starts at 8 MiB (like the aws cli).
	// The maximum part size is 5 GiB, which would in theory allow 50000 GiB (~48.8 TiB) in 10,000 parts.
	// The aws cli follows a very similar algorithm: https://github.com/boto/s3transfer/blob/0.5.0/s3transfer/utils.py#L711-L763
	var partSize int64 = 8 * MiB
	for 10000*partSize < fileSize {
		partSize *= 2
	}
	if partSize > 5*GiB {
		partSize = 5 * GiB
	}
	fmt.Printf("Part size: %s\n", formatFilesize((partSize)))
	fmt.Printf("The upload will consist of %d parts.\n", int64(math.Ceil(float64(fileSize)/float64(partSize))))
	if fileSize > 5*TiB {
		fmt.Println("Warning: File size is greater than 5 TiB. At the time of writing 5 TiB is the maximum object size.")
		fmt.Println("This program is not stopping you from proceeding in case the limit has been increased, but be warned!")
	}
	if 10000*partSize < fileSize {
		fmt.Println("Warning: File size is too large to be transferred in 10,000 parts!")
	}

	// Open the file
	f, _ := os.Open(file)
	defer f.Close()

	// Look for a SHA256SUMS file and get this file's hash
	_, err = os.Stat("SHA256SUMS")
	if !errors.Is(err, fs.ErrNotExist) {
		sum, err := getSha256Sum("SHA256SUMS", file)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		} else if sum == "" {
			fmt.Fprintln(os.Stderr, "Warning: SHA256SUMS file is present but does not have an entry for this file.")
		} else {
			if createMultipartUploadInput.Metadata == nil {
				createMultipartUploadInput.Metadata = make(map[string]string)
			}
			createMultipartUploadInput.Metadata["sha256sum"] = sum
		}
	}

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
		outputCreateMultipartUpload, err := client.CreateMultipartUpload(context.TODO(), &createMultipartUploadInput)
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

		// Check if there are any gaps in the existing parts
		partNumbers := make([]int, len(parts))
		for i, part := range parts {
			partNumbers[i] = int(part.PartNumber)
		}
		sort.Ints(partNumbers)
		for i, partNumber := range partNumbers {
			if partNumber != i+1 {
				fmt.Fprintf(os.Stderr, "Error: existing parts are not contiguous (part %d is missing). Can not handle this case yet.\n", i+1)
				os.Exit(1)
			}
		}

		if offset > fileSize {
			fmt.Println("Error: size of parts already uploaded is greater than local file size.")
			os.Exit(1)
		}
	}

	// Control variables
	var oldRate int64
	interrupted := false
	paused := false
	waitingToUnpause := false

	// Trap Ctrl-C signal
	signalChannel := make(chan os.Signal, 1)
	signal.Notify(signalChannel, os.Interrupt)
	go func() {
		for sig := range signalChannel {
			if sig != os.Interrupt {
				continue
			}
			if waitingToUnpause {
				os.Exit(1)
			}
			if interrupted {
				os.Exit(1)
			}
			interrupted = true
			fmt.Println("\nInterrupt received, finishing current part. Press Ctrl-C again to exit immediately. Press the space key to cancel exit.")
		}
	}()

	// Attempt to configure the terminal so that single characters can be read from stdin
	stdinFd := os.Stdin.Fd()
	oldState, err := configureTerminal(stdinFd)
	if err == nil {
		defer func() {
			restoreTerminal(stdinFd, oldState)
		}()
	} else {
		fmt.Fprintln(os.Stderr, "Warning: could not configure terminal. You have to use the enter key after each keyboard input.")
		fmt.Fprintln(os.Stderr, err)
	}
	// Send characters from stdin to a channel
	stdinInput := make(chan rune, 1)
	go func() {
		stdinReader := bufio.NewReader(os.Stdin)
		for {
			char, _, err := stdinReader.ReadRune()
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
			}
			stdinInput <- char
		}
	}()

	fmt.Println("Tip: Press ? to see the available keyboard controls.")

	for offset < fileSize {
		for paused {
			waitingToUnpause = true
			fmt.Println("Transfer is paused. Press the space key to resume.")
			r := <-stdinInput
			if r == ' ' {
				fmt.Println("Resuming...")
				paused = false
				waitingToUnpause = false
			}
		}

		partNumber += 1
		partStartTime := time.Now()
		partData := make([]byte, min(partSize, fileSize-offset))
		n, err := f.ReadAt(partData, offset)
		if err != nil && err != io.EOF {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		reader := flowrate.NewReader(bytes.NewReader(partData), rate)
		reader.SetTransferSize(int64(len(partData)))
		reader.SetTotal(offset, fileSize)

		// Start the upload in a go routine
		doneCh := make(chan struct{})
		var outputUploadPart *s3.UploadPartOutput
		go func() {
			defer close(doneCh)
			outputUploadPart, err = client.UploadPart(context.TODO(), &s3.UploadPartInput{
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
		}()

		// Main loop while the upload is in progress
		for doneCh != nil {
			select {
			case <-doneCh:
				doneCh = nil
			case <-time.After(time.Second):
			case r := <-stdinInput:
				if r == 'u' {
					rate = 0
					reader.SetLimit(rate)
					fmt.Printf("\nUnlimited transfer rate.\n")
				} else if r == 'r' {
					rate = initialRate
					reader.SetLimit(rate)
					if rate == 0 {
						fmt.Printf("\nUnlimited transfer rate.")
					} else {
						fmt.Printf("\nTransfer rate set to: %s/s.", formatSize(rate))
					}
				} else if r == 'a' || r == 's' || r == 'd' || r == 'f' ||
					r == 'z' || r == 'x' || r == 'c' || r == 'v' {
					if rate <= 1e3 && r != 'a' {
						rate = 0
					}
					if r == 'a' {
						rate += 1e3
					} else if r == 's' {
						rate += 10e3
					} else if r == 'd' {
						rate += 100e3
					} else if r == 'f' {
						rate += 250e3
					} else if r == 'z' {
						rate -= 1e3
					} else if r == 'x' {
						rate -= 10e3
					} else if r == 'c' {
						rate -= 100e3
					} else if r == 'v' {
						rate -= 250e3
					}
					if rate < 1e3 {
						rate = 1e3
					}
					reader.SetLimit(rate)
					fmt.Printf("\nTransfer rate set to: %s/s\n", formatSize(rate))
				} else if r >= '0' && r <= '9' {
					n := int64(r - '0')
					if n == 0 {
						rate = 1e6
					} else {
						rate = n * 100e3
					}
					reader.SetLimit(rate)
					fmt.Printf("\nTransfer rate set to: %s/s\n", formatSize(rate))
				} else if r == 'p' {
					// Pause after current part
					paused = !paused
					if paused {
						fmt.Println("\nTransfer will pause after the current part.")
					} else {
						fmt.Println("\nWill not pause.")
					}
				} else if r == ' ' {
					// Pausing with the space key just lowers the rate to be very low
					// Unpausing restores the previous rate
					if interrupted {
						interrupted = false
						fmt.Println("\nExit cancelled.")
					} else {
						paused = !paused
						if paused {
							oldRate = rate
							rate = 1e3
						} else {
							rate = oldRate
						}
						reader.SetLimit(rate)
						if rate == 0 {
							fmt.Printf("\nUnlimited transfer rate.")
						} else {
							fmt.Printf("\nTransfer rate set to: %s/s.", formatSize(rate))
						}
						if paused {
							fmt.Print(" Transfer will pause after the current part.")
						}
						fmt.Println()
					}
				} else if r == '?' {
					fmt.Println()
					fmt.Println()
					fmt.Println("u       - set to unlimited transfer rate")
					fmt.Println("r       - restore initial transfer rate (from --bwlimit)")
					fmt.Println("a s d f - increase transfer rate by 1, 10, 100, or 250 kB/s")
					fmt.Println("z x c v - decrease transfer rate by 1, 10, 100, or 250 kB/s")
					fmt.Println("0-9     - set the transfer rate to 0.X MB/s")
					fmt.Println("p       - pause transfer after current part")
					fmt.Println("[space] - pause transfer (decreases transfer rate to 1 kB/s)")
					fmt.Println("Ctrl-C  - exit after current part")
					fmt.Println("          press twice to abort immediately")
					fmt.Println()
				} else if r == '\n' {
					fmt.Println()
				} else {
					fmt.Printf("\ninput: %+v\n", r)
				}
			}

			var targetRate string
			if rate != 0 {
				targetRate = fmt.Sprintf(" (target: %s/s)", formatSize(rate))
			}

			s := reader.Status()
			fmt.Printf("\033[2K\rUploading part %d (%d bytes).. %s, %s/s%s, %s remaining (total: %s, %s remaining)", partNumber, len(partData), s.Progress, formatSize(s.CurRate), targetRate, s.TimeRem.Round(time.Second), s.TotalProgress, s.TotalTimeRem.Round(time.Second))
		}

		fmt.Printf("\033[2K\rUploaded part %d (%d bytes) in %s.\n", partNumber, len(partData), time.Since(partStartTime).Round(time.Second))

		// Check if the user wants to stop
		if interrupted {
			fmt.Println("Exited early.")
			os.Exit(1)
		}

		parts = append(parts, s3Types.CompletedPart{
			ETag:       outputUploadPart.ETag,
			PartNumber: partNumber,
		})
		offset += int64(n)
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
