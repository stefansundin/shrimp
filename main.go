package main

import (
	"bufio"
	"context"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base32"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"sort"
	"strings"
	"time"

	flag "github.com/stefansundin/go-zflag"
	"github.com/stefansundin/shrimp/flowrate"
	"github.com/stefansundin/shrimp/terminal"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3Types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

const version = "0.2.0"

var useDualStackEndpoint aws.DualStackEndpointState

func init() {
	// Do not fail if a region is not specified anywhere
	// This is only used for the first call that looks up the bucket region
	if _, present := os.LookupEnv("AWS_DEFAULT_REGION"); !present {
		os.Setenv("AWS_DEFAULT_REGION", "us-east-1")
	}
	// Dual-stack endpoint configuration
	if v, ok := os.LookupEnv("AWS_USE_DUALSTACK_ENDPOINT"); ok {
		if v == "true" {
			useDualStackEndpoint = aws.DualStackEndpointStateEnabled
		} else {
			useDualStackEndpoint = aws.DualStackEndpointStateDisabled
		}
	}
}

func main() {
	exitCode, err := run()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	os.Exit(exitCode)
}

func run() (int, error) {
	var profile, region, bwlimit, partSizeRaw, endpointURL, caBundle, scheduleFn, cacheControl, contentDisposition, contentEncoding, contentLanguage, contentType, expectedBucketOwner, tagging, storageClass, metadata, requestPayer, sse, sseCustomerAlgorithm, sseCustomerKey, sseKmsKeyId, checksumAlgorithm, objectLockLegalHoldStatus, objectLockMode, objectLockRetainUntilDate string
	var bucketKeyEnabled, computeChecksum, noVerifySsl, noSignRequest, useAccelerateEndpoint, usePathStyle, mfaSecretFlag, force, dryrun, debug, versionFlag bool
	var mfaDuration time.Duration
	var mfaSecret []byte
	flag.StringVar(&profile, "profile", "", "Use a specific profile from your credential file.")
	flag.StringVar(&region, "region", "", "The bucket region. Avoids one API call.")
	flag.StringVar(&bwlimit, "bwlimit", "", "Bandwidth limit. (e.g. \"2.5m\")")
	flag.StringVar(&partSizeRaw, "part-size", "", "Override automatic part size. (e.g. \"128m\")")
	flag.StringVar(&endpointURL, "endpoint-url", "", "Override the S3 endpoint URL. (for use with S3 compatible APIs)")
	flag.StringVar(&caBundle, "ca-bundle", "", "The CA certificate bundle to use when verifying SSL certificates.")
	flag.StringVar(&scheduleFn, "schedule", "", "Schedule file to use for automatically adjusting the bandwidth limit (see https://github.com/stefansundin/shrimp/discussions/4).")
	flag.StringVar(&cacheControl, "cache-control", "", "Specifies caching behavior for the object.")
	flag.StringVar(&contentDisposition, "content-disposition", "", "Specifies presentational information for the object.")
	flag.StringVar(&contentEncoding, "content-encoding", "", "Specifies what content encodings have been applied to the object.")
	flag.StringVar(&contentLanguage, "content-language", "", "Specifies the language the content is in.")
	flag.StringVar(&contentType, "content-type", "", "A standard MIME type describing the format of the object data.")
	flag.StringVar(&expectedBucketOwner, "expected-bucket-owner", "", "The account ID of the expected bucket owner.")
	flag.StringVar(&tagging, "tagging", "", "The tag-set for the object. The tag-set must be encoded as URL Query parameters.")
	flag.StringVar(&storageClass, "storage-class", "", "Storage class. Known values: "+strings.Join(knownStorageClasses(), ", ")+".")
	flag.StringVar(&metadata, "metadata", "", "A map of metadata to store with the object in S3. (JSON syntax is not supported)")
	flag.StringVar(&requestPayer, "request-payer", "", "Confirms that the requester knows that they will be charged for the requests. Possible values: requester.")
	flag.StringVar(&sse, "sse", "", "Specifies server-side encryption of the object in S3. Possible values: AES256, aws:kms, aws:kms:dsse.")
	flag.StringVar(&sseCustomerAlgorithm, "sse-c", "", "Specifies server-side encryption using customer provided keys of the the object in S3. AES256 is the only valid value. If you provide this value, --sse-c-key must be specified as well.")
	flag.StringVar(&sseCustomerKey, "sse-c-key", "", "The customer-provided encryption key to use to server-side encrypt the object in S3. The key provided should not be base64 encoded.")
	flag.StringVar(&sseKmsKeyId, "sse-kms-key-id", "", "The customer-managed AWS Key Management Service (KMS) key ID that should be used to server-side encrypt the object in S3.")
	flag.StringVar(&checksumAlgorithm, "checksum-algorithm", "", "The checksum algorithm to use for the object. Supported values: CRC32, CRC32C, SHA1, SHA256.")
	flag.StringVar(&objectLockLegalHoldStatus, "object-lock-legal-hold-status", "", "Specifies whether a legal hold will be applied to this object. Possible values: ON, OFF.")
	flag.StringVar(&objectLockMode, "object-lock-mode", "", "The Object Lock mode that you want to apply to this object. Possible values: GOVERNANCE, COMPLIANCE.")
	flag.StringVar(&objectLockRetainUntilDate, "object-lock-retain-until-date", "", "The date and time when you want this object's Object Lock to expire. Must be formatted as a timestamp parameter. (e.g. \"2022-03-14T15:14:15Z\")")
	flag.DurationVar(&mfaDuration, "mfa-duration", time.Hour, "MFA duration. shrimp will prompt for another code after this duration. (max \"12h\")")
	flag.BoolVar(&bucketKeyEnabled, "bucket-key-enabled", false, "Enables use of an S3 Bucket Key for object encryption with server-side encryption using AWS KMS (SSE-KMS).")
	flag.BoolVar(&mfaSecretFlag, "mfa-secret", false, "Provide the MFA secret and shrimp will automatically generate TOTP codes. (useful if the upload takes longer than the allowed assume role duration)")
	flag.BoolVar(&computeChecksum, "compute-checksum", false, "Compute checksum and add to SHA256SUMS file.")
	flag.BoolVar(&noVerifySsl, "no-verify-ssl", false, "Do not verify SSL certificates.")
	flag.BoolVar(&noSignRequest, "no-sign-request", false, "Do not sign requests. This does not work with Amazon S3, but may work with other S3 APIs.")
	flag.BoolVar(&useAccelerateEndpoint, "use-accelerate-endpoint", false, "Use S3 Transfer Acceleration.")
	flag.BoolVar(&usePathStyle, "use-path-style", false, "Use S3 Path Style.")
	flag.BoolVar(&force, "force", false, "Overwrite existing object.")
	flag.BoolVar(&dryrun, "dryrun", false, "Checks if the upload was started previously and how much was completed. (use in combination with --bwlimit to calculate remaining time)")
	flag.BoolVar(&debug, "debug", false, "Turn on debug logging.")
	flag.BoolVar(&versionFlag, "version", false, "Print version number.")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "shrimp version %s\n", version)
		fmt.Fprintln(os.Stderr, "Copyright (C) 2022 Stefan Sundin")
		fmt.Fprintln(os.Stderr, "Website: https://github.com/stefansundin/shrimp")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "shrimp comes with ABSOLUTELY NO WARRANTY.")
		fmt.Fprintln(os.Stderr, "This is free software, and you are welcome to redistribute it under certain")
		fmt.Fprintln(os.Stderr, "conditions. See the GNU General Public Licence version 3 for details.")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "Usage: %s [parameters] <LocalPath> <S3Uri>\n", os.Args[0])
		fmt.Fprintln(os.Stderr, "LocalPath must be a local file.")
		fmt.Fprintln(os.Stderr, "S3Uri must have the format s3://<bucketname>/<key>.")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Parameters:")
		flag.PrintDefaults()
	}
	flag.Parse()

	if versionFlag {
		fmt.Println(version)
		return 0, nil
	} else if flag.NArg() < 2 {
		flag.Usage()
		fmt.Fprintln(os.Stderr)
		return 1, errors.New("Error: LocalPath and S3Uri parameters are required!")
	} else if flag.NArg() > 2 {
		flag.Usage()
		fmt.Fprintln(os.Stderr)
		return 1, errors.New("Error: Too many positional arguments!")
	}

	if endpointURL != "" {
		if !strings.HasPrefix(endpointURL, "http://") && !strings.HasPrefix(endpointURL, "https://") {
			return 1, errors.New("Error: The endpoint URL must start with http:// or https://.")
		}
		if !usePathStyle {
			u, err := url.Parse(endpointURL)
			if err != nil {
				return 1, errors.New("Error: Unable to parse the endpoint URL.")
			}
			hostname := u.Hostname()
			if hostname == "localhost" || net.ParseIP(hostname) != nil {
				if debug {
					fmt.Fprintln(os.Stderr, "Detected IP address in endpoint URL. Implicitly opting in to path style.")
				}
				usePathStyle = true
			}
		}
	}
	if mfaDuration > 12*time.Hour {
		fmt.Fprintln(os.Stderr, "Warning: MFA duration can not exceed 12 hours.")
	}
	if mfaSecretFlag {
		fmt.Fprintln(os.Stderr, "Read more about the --mfa-secret feature here: https://github.com/stefansundin/shrimp/discussions/3")
		secret, ok := os.LookupEnv("AWS_MFA_SECRET")
		if ok {
			fmt.Fprintln(os.Stderr, "MFA secret read from AWS_MFA_SECRET.")
		} else {
			fmt.Fprint(os.Stderr, "MFA secret: ")
			_, err := fmt.Scanln(&secret)
			fmt.Fprint(os.Stderr, "\033[1A\033[2K") // erase the line
			if err != nil {
				return 1, err
			}
		}
		fmt.Fprintln(os.Stderr)
		// Normalize secret
		secret = strings.TrimSpace(secret)
		if n := len(secret) % 8; n != 0 {
			secret = secret + strings.Repeat("=", 8-n)
		}
		secret = strings.ToUpper(secret)
		var err error
		mfaSecret, err = base32.StdEncoding.DecodeString(secret)
		if err != nil {
			return 1, errors.New("Error: Invalid MFA secret.")
		}
	}
	file := flag.Arg(0)
	bucket, key := parseS3Uri(flag.Arg(1))
	if strings.HasPrefix(file, "s3://") {
		return 1, errors.New("Error: shrimp is currently not able to copy files from S3.")
	}
	if bucket == "" || key == "" {
		return 1, errors.New("Error: The destination must have the format s3://<bucketname>/<key>")
	}

	// Construct the CreateMultipartUploadInput data
	createMultipartUploadInput := s3.CreateMultipartUploadInput{
		Bucket:                    aws.String(bucket),
		Key:                       aws.String(key),
		BucketKeyEnabled:          aws.Bool(bucketKeyEnabled),
		CacheControl:              aws.String(cacheControl),
		ChecksumAlgorithm:         s3Types.ChecksumAlgorithm(checksumAlgorithm),
		ContentDisposition:        aws.String(contentDisposition),
		ContentEncoding:           aws.String(contentEncoding),
		ContentLanguage:           aws.String(contentLanguage),
		ContentType:               aws.String(contentType),
		ExpectedBucketOwner:       aws.String(expectedBucketOwner),
		ObjectLockLegalHoldStatus: s3Types.ObjectLockLegalHoldStatus(objectLockLegalHoldStatus),
		ObjectLockMode:            s3Types.ObjectLockMode(objectLockMode),
		RequestPayer:              s3Types.RequestPayer(requestPayer),
		ServerSideEncryption:      s3Types.ServerSideEncryption(sse),
		SSECustomerAlgorithm:      aws.String(sseCustomerAlgorithm),
		SSECustomerKey:            aws.String(sseCustomerKey),
		SSEKMSKeyId:               aws.String(sseKmsKeyId),
		Tagging:                   aws.String(tagging),
	}
	if storageClass != "" {
		createMultipartUploadInput.StorageClass = s3Types.StorageClass(storageClass)
		if createMultipartUploadInput.StorageClass == s3Types.StorageClassReducedRedundancy {
			fmt.Fprintln(os.Stderr, "Warning: REDUCED_REDUNDANCY is not recommended for use. It no longer has any cost benefits over STANDARD.")
			if dryrun {
				fmt.Fprintln(os.Stderr)
			} else {
				fmt.Fprintln(os.Stderr, "Press enter to continue anyway.")
				fmt.Scanln()
			}
		}
	}
	if metadata != "" {
		if m, err := parseMetadata(metadata); err == nil {
			createMultipartUploadInput.Metadata = m
		} else {
			return 1, fmt.Errorf("Error: %w", err)
		}
	}
	if objectLockRetainUntilDate != "" {
		t, err := parseTimestamp(objectLockRetainUntilDate)
		if err != nil {
			return 1, err
		}
		createMultipartUploadInput.ObjectLockRetainUntilDate = t
	}

	var initialRate int64
	if bwlimit != "" {
		var err error
		initialRate, err = parseRate(bwlimit)
		if err != nil {
			return 1, err
		}
	}
	var schedule *Schedule
	if scheduleFn != "" {
		var err error
		schedule, err = readSchedule(scheduleFn)
		if err != nil {
			return 1, fmt.Errorf("Error loading %s: %w", scheduleFn, err)
		}
		if bwlimit != "" {
			schedule.defaultRate = initialRate
		} else if schedule.defaultRate != 0 {
			initialRate = schedule.defaultRate
		}
	}
	rate := initialRate

	// Get the file size
	// TODO: Check if the file has been modified since the multi part was started and print a warning
	stat, err := os.Stat(file)
	if err != nil {
		return 1, err
	}
	fileSize := stat.Size()
	fmt.Fprintf(os.Stderr, "File size: %s\n", formatFilesize(fileSize))
	if fileSize > 5*TiB {
		fmt.Fprintln(os.Stderr, "Warning: File size is greater than 5 TiB. At the time of writing 5 TiB is the maximum object size on Amazon S3.")
		fmt.Fprintln(os.Stderr, "This program is not stopping you from proceeding in case the limit has been increased, but be warned!")
	}

	var partSize int64 = 8 * MiB
	if partSizeRaw != "" {
		var err error
		partSize, err = parseFilesize(partSizeRaw)
		if err != nil {
			return 1, err
		}
	} else {
		// Detect best part size
		// Double the part size until the file fits in 10,000 parts.
		// The minimum part size is 5 MiB (except for the last part), although shrimp starts at 8 MiB (like the aws cli).
		// The maximum part size is 5 GiB, which would in theory allow 50000 GiB (~48.8 TiB) in 10,000 parts.
		// The aws cli follows a very similar algorithm: https://github.com/boto/s3transfer/blob/0.5.0/s3transfer/utils.py#L711-L763
		// var partSize int64 = 8 * MiB
		for 10000*partSize < fileSize {
			partSize *= 2
		}
		if partSize > 5*GiB {
			partSize = 5 * GiB
		}
	}
	fmt.Fprintf(os.Stderr, "Part size: %s\n", formatFilesize(partSize))
	if partSize < 5*MiB || partSize > 5*GiB {
		fmt.Fprintln(os.Stderr, "Warning: Part size is not in the allowed limits (must be between 5 MiB to 5 GiB).")
		fmt.Fprintln(os.Stderr, "This program is not stopping you from proceeding in case the limits have changed, but be warned!")
	}
	fmt.Fprintf(os.Stderr, "The upload will consist of %d parts.\n", int64(math.Ceil(float64(fileSize)/float64(partSize))))
	if 10000*partSize < fileSize {
		fmt.Fprintln(os.Stderr, "Warning: File size is too large to be transferred in 10,000 parts!")
	}
	fmt.Fprintln(os.Stderr)

	// Open the file
	f, err := os.Open(file)
	if err != nil {
		return 1, err
	}
	defer f.Close()

	// Look for a SHA256SUMS file and get this file's hash
	if !dryrun {
		_, err = os.Stat("SHA256SUMS")
		if !errors.Is(err, fs.ErrNotExist) {
			sum, err := lookupChecksum("SHA256SUMS", file)
			if err != nil {
				return 1, fmt.Errorf("Error: %w", err)
			} else if sum == "" {
				if !computeChecksum {
					fmt.Fprintln(os.Stderr, "Warning: SHA256SUMS file is present but does not have an entry for this file. Consider using --compute-checksum.")
				}
			} else {
				if createMultipartUploadInput.Metadata == nil {
					createMultipartUploadInput.Metadata = make(map[string]string)
				}
				createMultipartUploadInput.Metadata["sha256sum"] = sum
			}
		}
		if computeChecksum && createMultipartUploadInput.Metadata["sha256sum"] == "" {
			fmt.Fprint(os.Stderr, "Computing SHA256 checksum... ")
			sum, err := computeSha256Sum(file)
			if err != nil {
				return 1, err
			}
			if createMultipartUploadInput.Metadata == nil {
				createMultipartUploadInput.Metadata = make(map[string]string)
			}
			createMultipartUploadInput.Metadata["sha256sum"] = sum
			fmt.Fprintln(os.Stderr, sum)
			fmt.Fprintln(os.Stderr, "Adding checksum to SHA256SUMS...")
			sumsFile, err := os.OpenFile("SHA256SUMS", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				return 1, fmt.Errorf("Error adding checksum to SHA256SUMS: %w", err)
			}
			defer sumsFile.Close()
			line := fmt.Sprintf("%s  %s\n", sum, file)
			_, err = sumsFile.WriteString(line)
			if err != nil {
				return 1, fmt.Errorf("Error adding checksum to SHA256SUMS: %w", err)
			}
		}
	}

	// Initialize the AWS SDK
	var promptingForMfa bool
	var mfaReader io.Reader = os.Stdin
	var mfaWriter io.Writer
	cfg, err := config.LoadDefaultConfig(
		context.TODO(),
		func(o *config.LoadOptions) error {
			if profile != "" {
				o.SharedConfigProfile = profile
			}
			if caBundle != "" {
				f, err := os.Open(caBundle)
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
					os.Exit(1)
				}
				o.CustomCABundle = f
			}
			if noVerifySsl {
				o.HTTPClient = &http.Client{
					Transport: &http.Transport{
						TLSClientConfig: &tls.Config{
							InsecureSkipVerify: true,
						},
					},
				}
			}
			if debug {
				var lm aws.ClientLogMode = aws.LogRequest | aws.LogResponse
				o.ClientLogMode = &lm
			}
			return nil
		},
		config.WithAssumeRoleCredentialOptions(func(o *stscreds.AssumeRoleOptions) {
			o.Duration = mfaDuration
			o.TokenProvider = func() (string, error) {
				if mfaSecret == nil {
					promptingForMfa = true
					for {
						fmt.Fprint(os.Stderr, "Assume Role MFA token code: ")
						var code string
						_, err = fmt.Fscanln(mfaReader, &code)
						if len(code) == 6 && isNumeric(code) {
							promptingForMfa = false
							return code, err
						}
						fmt.Fprintln(os.Stderr, "Code must consist of 6 digits. Please try again.")
					}
				} else {
					t := time.Now().UTC()
					period := 30
					counter := uint64(math.Floor(float64(t.Unix()) / float64(period)))
					code, err := generateOTP(mfaSecret, counter, sha1.New, 6)
					if debug {
						fmt.Fprintf(os.Stderr, "Generated TOTP code: %s\n", code)
					}
					if err != nil {
						fmt.Fprintln(os.Stderr, err)
					}
					return code, err
				}
			}
		}),
	)
	if err != nil {
		return 1, err
	}
	client := s3.NewFromConfig(cfg,
		func(o *s3.Options) {
			o.EndpointOptions.UseDualStackEndpoint = useDualStackEndpoint
			if noSignRequest {
				o.Credentials = aws.AnonymousCredentials{}
			}
			if region != "" {
				o.Region = region
			}
			if endpointURL != "" {
				o.BaseEndpoint = aws.String(endpointURL)
			}
			if usePathStyle {
				o.UsePathStyle = true
			}
			if useAccelerateEndpoint {
				o.UseAccelerate = true
			}
		})
	encryptedEndpoint := (endpointURL == "" || strings.HasPrefix(endpointURL, "https://"))

	// Get the bucket location
	if endpointURL == "" && region == "" {
		bucketLocationOutput, err := client.GetBucketLocation(context.TODO(), &s3.GetBucketLocationInput{
			Bucket: aws.String(bucket),
		})
		if err != nil {
			return 1, err
		}
		bucketRegion := normalizeBucketLocation(bucketLocationOutput.LocationConstraint)
		if debug {
			fmt.Fprintf(os.Stderr, "Bucket region: %s\n", bucketRegion)
		}
		client = s3.NewFromConfig(cfg, func(o *s3.Options) {
			o.EndpointOptions.UseDualStackEndpoint = useDualStackEndpoint
			if noSignRequest {
				o.Credentials = aws.AnonymousCredentials{}
			}
			o.Region = bucketRegion
			if usePathStyle {
				o.UsePathStyle = true
			}
			if useAccelerateEndpoint {
				o.UseAccelerate = true
			}
		})
	}

	// Abort if the object already exists
	if !force {
		obj, err := client.HeadObject(context.TODO(), &s3.HeadObjectInput{
			Bucket:       aws.String(bucket),
			Key:          aws.String(key),
			RequestPayer: s3Types.RequestPayer(requestPayer),
		})
		if obj != nil || err == nil || !isSmithyErrorCode(err, 404) {
			if obj != nil {
				fmt.Fprintln(os.Stderr, "The object already exists in the S3 bucket.")
				fmt.Fprintln(os.Stderr, "Please delete it or use --force to overwrite the existing object.")
			}
			return 1, err
		}
	}

	// Check if we should resume an upload
	fmt.Fprintln(os.Stderr, "Checking if this upload is already in progress.")
	var uploadId string
	paginatorListMultipartUploads := s3.NewListMultipartUploadsPaginator(client, &s3.ListMultipartUploadsInput{
		Bucket:       aws.String(bucket),
		RequestPayer: s3Types.RequestPayer(requestPayer),
	})
	for paginatorListMultipartUploads.HasMorePages() {
		page, err := paginatorListMultipartUploads.NextPage(context.TODO())
		if err != nil {
			return 1, err
		}
		for _, upload := range page.Uploads {
			if aws.ToString(upload.Key) != key {
				continue
			}

			// fmt.Fprintf(os.Stderr, "Upload: {Key: %s, Initiated: %s, Initiator: {%s %s}, Owner: {%s %s}, StorageClass: %s, UploadId: %s}\n", aws.ToString(upload.Key), upload.Initiated, aws.ToString(upload.Initiator.DisplayName), aws.ToString(upload.Initiator.ID), aws.ToString(upload.Owner.DisplayName), aws.ToString(upload.Owner.ID), upload.StorageClass, aws.ToString(upload.UploadId))
			if uploadId != "" {
				return 1, errors.New("Error: More than one upload for this key is in progress. Please manually abort duplicated multipart uploads.")
			}
			uploadId = aws.ToString(upload.UploadId)
			fmt.Fprintf(os.Stderr, "Found an upload in progress with upload id: %s\n", uploadId)

			localLocation, err := time.LoadLocation("Local")
			if err != nil {
				return 1, err
			}
			fmt.Fprintf(os.Stderr, "Upload started at %v.\n", upload.Initiated.In(localLocation))

			if createMultipartUploadInput.StorageClass != "" &&
				upload.StorageClass != createMultipartUploadInput.StorageClass {
				return 1, fmt.Errorf("Error: Existing upload uses the storage class %s. You requested %s. Either make them match or remove --storage-class.\n", upload.StorageClass, createMultipartUploadInput.StorageClass)
			}
		}
	}

	// Create the multipart upload or get the part information from an existing upload
	parts := []s3Types.CompletedPart{}
	var partNumber int32 = 1
	var offset int64
	if uploadId == "" {
		if dryrun {
			fmt.Fprintln(os.Stderr, "Upload not started.")
		} else {
			fmt.Fprintln(os.Stderr, "Creating multipart upload.")
			outputCreateMultipartUpload, err := client.CreateMultipartUpload(context.TODO(), &createMultipartUploadInput)
			if err != nil {
				return 1, err
			}

			uploadId = aws.ToString(outputCreateMultipartUpload.UploadId)
			fmt.Fprintf(os.Stderr, "Upload id: %v\n", uploadId)
		}
	} else {
		paginatorListParts := s3.NewListPartsPaginator(client, &s3.ListPartsInput{
			Bucket:       aws.String(bucket),
			Key:          aws.String(key),
			UploadId:     aws.String(uploadId),
			RequestPayer: s3Types.RequestPayer(requestPayer),
		})
		var part1Size int64
		for paginatorListParts.HasMorePages() {
			page, err := paginatorListParts.NextPage(context.TODO())
			if err != nil {
				return 1, err
			}
			if part1Size == 0 && len(page.Parts) > 0 {
				part1Size = aws.ToInt64(page.Parts[0].Size)
			}
			partNumber += int32(len(page.Parts))
			for _, part := range page.Parts {
				if debug {
					fmt.Fprintf(os.Stderr, "Part: %s\n", string(jsonMustMarshal(part)))
				}
				partNumber := aws.ToInt32(part.PartNumber)
				partSize := aws.ToInt64(part.Size)
				offset += partSize
				parts = append(parts, s3Types.CompletedPart{
					PartNumber:     part.PartNumber,
					ETag:           part.ETag,
					ChecksumCRC32:  part.ChecksumCRC32,
					ChecksumCRC32C: part.ChecksumCRC32C,
					ChecksumSHA1:   part.ChecksumSHA1,
					ChecksumSHA256: part.ChecksumSHA256,
				})
				// Check for potential problems (if not the last part)
				if offset != fileSize {
					if partSize < 5*MiB {
						fmt.Fprintf(os.Stderr, "Warning: Part %d has size %s, which is less than 5 MiB, and it is not the last part in the upload. This upload will fail with an error!\n", partNumber, formatFilesize(partSize))
					} else if partSize != part1Size {
						fmt.Fprintf(os.Stderr, "Warning: Part %d has an inconsistent size (%d bytes) compared to part 1 (%d bytes).\n", partNumber, partSize, part1Size)
					}
				}
			}
		}
		fmt.Fprintf(os.Stderr, "%s already uploaded in %d parts.\n", formatFilesize(offset), len(parts))

		// Check if there are any gaps in the existing parts
		partNumbers := make([]int, len(parts))
		for i, part := range parts {
			partNumbers[i] = int(aws.ToInt32(part.PartNumber))
		}
		sort.Ints(partNumbers)
		for i, partNumber := range partNumbers {
			if partNumber != i+1 {
				return 1, fmt.Errorf("Error: Existing parts are not contiguous (part %d is missing). Can not handle this case yet.\n", i+1)
			}
		}

		if offset > fileSize {
			return 1, errors.New("Error: Size of parts already uploaded is greater than local file size.")
		}
		fmt.Fprintf(os.Stderr, "%s remaining.\n", formatFilesize(fileSize-offset))
	}

	if dryrun {
		if rate != 0 {
			bytesRemaining := fileSize - offset
			ns := float64(bytesRemaining) / float64(rate) * 1e9
			timeRemaining := time.Duration(ns).Round(time.Second)
			fmt.Fprintf(os.Stderr, "\nCompleting the upload at %s/s will take %s.\n", formatSize(rate), timeRemaining)
		}
		return 0, nil
	}

	// Attempt to configure the terminal so that single characters can be read from stdin
	oldTerminalState, err := terminal.ConfigureTerminal()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Warning: could not configure terminal. You have to use the enter key after each keyboard input.")
		fmt.Fprintln(os.Stderr, err)
	}
	defer func() {
		terminal.RestoreTerminal(oldTerminalState)
	}()
	// Send characters from stdin to a channel
	mfaReader, mfaWriter = io.Pipe()
	stdinInput := make(chan rune, 1)
	go func() {
		stdinReader := bufio.NewReader(os.Stdin)
		var mfaCode string
		for {
			char, _, err := stdinReader.ReadRune()
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
			}
			if promptingForMfa {
				// This code is only used if the user is prompted for MFA after the upload has started (i.e. after the terminal has been configured)
				// This looks a bit awkward but it is necessary since it is harder to reset the terminal and put back the rune that we already read
				if char >= '0' && char <= '9' {
					mfaCode += string(char)
					fmt.Fprint(os.Stderr, string(char))
				} else if (char == 127 || char == '\b') && len(mfaCode) > 0 {
					mfaCode = mfaCode[:len(mfaCode)-1]
					fmt.Fprint(os.Stderr, "\b\033[J")
				} else if char == '\n' || char == '\r' {
					fmt.Fprintln(os.Stderr)
					mfaWriter.Write([]byte(mfaCode + "\n"))
					mfaCode = ""
				}
				continue
			}
			stdinInput <- char
		}
	}()

	// Control variables
	var reader *flowrate.Reader
	var oldRate int64
	interrupted := false
	paused := false
	waitingToUnpause := false
	waitingAfterError := false

	// Trap Ctrl-C signal
	signalChannel := make(chan os.Signal, 1)
	signal.Notify(signalChannel, os.Interrupt)
	go func() {
		for sig := range signalChannel {
			if sig != os.Interrupt {
				continue
			}
			if interrupted || waitingAfterError {
				if oldTerminalState != nil {
					terminal.RestoreTerminal(oldTerminalState)
				}
				fmt.Fprintln(os.Stderr)
				os.Exit(1)
			}
			interrupted = true
			if waitingToUnpause {
				stdinInput <- 'q'
				continue
			}
			fmt.Fprintln(os.Stderr, "\nInterrupt received, finishing current part. Press Ctrl-C again to exit immediately. Press the space key to cancel exit.")
		}
	}()

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Tip: Press ? to see the available keyboard controls.")

	// Start the scheduler
	if schedule != nil && len(schedule.blocks) > 0 {
		block := schedule.next()
		if block.active() {
			rate = block.rate
		}

		go func() {
			for {
				block := schedule.next()
				start, end := block.next()

				for time.Now().Before(start) {
					time.Sleep(minDuration(time.Minute, time.Until(start)))
				}

				if !paused && rate != block.rate {
					fmt.Fprintf(os.Stderr, "\nScheduler: set ratelimit to %s.\n", formatLimit2(block.rate))
					rate = block.rate
					if reader != nil {
						reader.SetLimit(rate)
					}
					fmt.Fprintln(os.Stderr)
				}

				for time.Now().Before(end) {
					time.Sleep(minDuration(time.Minute, time.Until(end)))
				}

				// Check if the next block is right after the one we just did, otherwise reset to defaultRate
				if !paused {
					block = schedule.next()
					if block.active() && rate != schedule.defaultRate {
						fmt.Fprintf(os.Stderr, "\nScheduler: reset ratelimit to default (%s).\n", formatLimit2(schedule.defaultRate))
						rate = schedule.defaultRate
						if reader != nil {
							reader.SetLimit(rate)
						}
					}
				}
			}
		}()
	}

	for offset < fileSize {
		runtime.GC()

		for paused {
			waitingToUnpause = true
			if interrupted {
				return 1, nil
			}
			fmt.Fprintln(os.Stderr, "Transfer is paused. Press the space key to resume.")
			r := <-stdinInput
			if r == ' ' {
				fmt.Fprintln(os.Stderr, "Resuming.")
				paused = false
				waitingToUnpause = false
			}
		}

		partStartTime := time.Now()
		size := min(partSize, fileSize-offset)
		reader = flowrate.NewReader(
			io.NewSectionReader(f, offset, size),
			rate,
			!encryptedEndpoint,
		)
		reader.SetTransferSize(size)
		reader.SetTotal(offset, fileSize)

		// Start the upload in a go routine
		doneCh := make(chan struct{})
		var uploadPart *s3.UploadPartOutput
		var uploadErr error
		go func() {
			defer close(doneCh)
			uploadPartInput := &s3.UploadPartInput{
				Bucket:               aws.String(bucket),
				Key:                  aws.String(key),
				UploadId:             aws.String(uploadId),
				PartNumber:           aws.Int32(partNumber),
				Body:                 reader,
				ChecksumAlgorithm:    s3Types.ChecksumAlgorithm(checksumAlgorithm),
				ExpectedBucketOwner:  aws.String(expectedBucketOwner),
				RequestPayer:         s3Types.RequestPayer(requestPayer),
				SSECustomerAlgorithm: aws.String(sseCustomerAlgorithm),
				SSECustomerKey:       aws.String(sseCustomerKey),
			}
			uploadPart, uploadErr = client.UploadPart(context.TODO(), uploadPartInput)
			if debug && uploadPart != nil {
				fmt.Fprintf(os.Stderr, "Part: %s\n", string(jsonMustMarshal(uploadPart)))
			}
		}()

		// Main loop while the upload is in progress
		var s flowrate.Status
		for doneCh != nil {
			select {
			case <-doneCh:
				doneCh = nil
			case <-time.After(time.Second):
			case r := <-stdinInput:
				if r == 'i' {
					fmt.Fprintln(os.Stderr)
					fmt.Fprintln(os.Stderr)
					fmt.Fprintf(os.Stderr, "Uploading %s to %s\n", flag.Arg(0), flag.Arg(1))
					fmt.Fprintf(os.Stderr, "File size: %s\n", formatFilesize(fileSize))
					fmt.Fprintf(os.Stderr, "Part size: %s\n", formatFilesize(partSize))
					if storageClass != "" {
						fmt.Fprintf(os.Stderr, "Storage class: %s\n", storageClass)
					}
					if scheduleFn != "" {
						fmt.Fprintf(os.Stderr, "Schedule: %s\n", scheduleFn)
					}
					fmt.Fprintf(os.Stderr, "Currently uploading part %d out of %d.\n", partNumber, int64(math.Ceil(float64(fileSize)/float64(partSize))))
					fmt.Fprintln(os.Stderr)
				} else if r == 'u' {
					rate = 0
					reader.SetLimit(rate)
					fmt.Fprint(os.Stderr, "\nUnlimited transfer rate.\n")
				} else if r == 'r' {
					rate = initialRate
					reader.SetLimit(rate)
					if rate == 0 {
						fmt.Fprint(os.Stderr, "\nUnlimited transfer rate.")
					} else {
						fmt.Fprintf(os.Stderr, "\nTransfer limit set to: %s/s.", formatSize(rate))
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
					fmt.Fprintf(os.Stderr, "\nTransfer limit set to: %s/s\n", formatSize(rate))
				} else if r >= '0' && r <= '9' {
					n := int64(r - '0')
					if n == 0 {
						rate = 1e6
					} else {
						rate = n * 100e3
					}
					reader.SetLimit(rate)
					fmt.Fprintf(os.Stderr, "\nTransfer limit set to: %s/s\n", formatSize(rate))
				} else if r == 'p' {
					// Pause after current part
					paused = !paused
					if paused {
						fmt.Fprintln(os.Stderr, "\nTransfer will pause after the current part.")
					} else {
						fmt.Fprintln(os.Stderr, "\nWill not pause.")
					}
				} else if r == ' ' {
					// Pausing with the space key just lowers the rate to be very low
					// Unpausing restores the old rate
					if interrupted {
						interrupted = false
						fmt.Fprintln(os.Stderr, "\nExit cancelled.")
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
							fmt.Fprint(os.Stderr, "\nUnlimited transfer rate.")
						} else {
							fmt.Fprintf(os.Stderr, "\nTransfer limit set to: %s/s.", formatSize(rate))
						}
						if paused {
							fmt.Fprint(os.Stderr, " Transfer will pause after the current part.")
						}
						fmt.Fprintln(os.Stderr)
					}
				} else if r == '?' {
					fmt.Fprintln(os.Stderr)
					fmt.Fprintln(os.Stderr)
					fmt.Fprintln(os.Stderr, "i       - print information about the upload")
					fmt.Fprintln(os.Stderr, "u       - set to unlimited transfer rate")
					fmt.Fprintln(os.Stderr, "r       - restore initial transfer limit (from --bwlimit)")
					fmt.Fprintln(os.Stderr, "a s d f - increase transfer limit by 1, 10, 100, or 250 kB/s")
					fmt.Fprintln(os.Stderr, "z x c v - decrease transfer limit by 1, 10, 100, or 250 kB/s")
					fmt.Fprintln(os.Stderr, "0-9     - limit the transfer rate to 0.X MB/s")
					fmt.Fprintln(os.Stderr, "p       - pause transfer after current part")
					fmt.Fprintln(os.Stderr, "[space] - pause transfer (sets transfer limit to 1 kB/s)")
					fmt.Fprintln(os.Stderr, "Ctrl-C  - exit after current part")
					fmt.Fprintln(os.Stderr, "          press twice to abort immediately")
					fmt.Fprintln(os.Stderr)
				} else if r == terminal.EnterKey {
					fmt.Fprintln(os.Stderr)
				}
			}

			for promptingForMfa {
				time.Sleep(time.Second)
			}

			s = reader.Status()
			fmt.Fprintf(os.Stderr, "\033[2K\rUploading part %d: %s, %s/s%s, %s remaining. (total: %s, %s remaining)", partNumber, s.Progress, formatSize(s.CurRate), formatLimit(rate, true), s.TimeRem.Round(time.Second), s.TotalProgress, s.TotalTimeRem.Round(time.Second))
		}

		// Part upload has completed or failed
		if uploadErr == nil {
			timeElapsed := niceDuration(time.Since(partStartTime))
			fmt.Fprintf(os.Stderr, "\033[2K\rUploaded part %d in %s (%s/s%s). (total: %s, %s remaining)\n", partNumber, timeElapsed, formatSize(s.CurRate), formatLimit(rate, false), s.TotalProgress, s.TotalTimeRem.Round(time.Second))

			// Check if the user wants to stop
			if interrupted {
				return 1, errors.New("Exited early.")
			}

			parts = append(parts, s3Types.CompletedPart{
				PartNumber:     aws.Int32(partNumber),
				ETag:           uploadPart.ETag,
				ChecksumCRC32:  uploadPart.ChecksumCRC32,
				ChecksumCRC32C: uploadPart.ChecksumCRC32C,
				ChecksumSHA1:   uploadPart.ChecksumSHA1,
				ChecksumSHA256: uploadPart.ChecksumSHA256,
			})
			offset += size
			partNumber += 1
		} else {
			fmt.Fprintln(os.Stderr)
			fmt.Fprintln(os.Stderr)
			fmt.Fprintf(os.Stderr, "Error uploading part %d: %v\n", partNumber, uploadErr)
			if interrupted {
				return 1, nil
			}
			fmt.Fprintln(os.Stderr, "Waiting 10 seconds and then retrying.")
			fmt.Fprintln(os.Stderr)
			waitingAfterError = true
			time.Sleep(10 * time.Second)
			waitingAfterError = false
		}
	}
	signal.Reset(os.Interrupt)

	// Do a sanity check
	if offset != fileSize {
		return 1, fmt.Errorf("Something went terribly wrong (offset != fileSize => %d != %d).", offset, fileSize)
	}

	// Complete the upload
	fmt.Fprintln(os.Stderr, "Completing the multipart upload.")
	completeMultipartUploadInput := &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(bucket),
		Key:      aws.String(key),
		UploadId: aws.String(uploadId),
		MultipartUpload: &s3Types.CompletedMultipartUpload{
			Parts: parts,
		},
		ExpectedBucketOwner:  aws.String(expectedBucketOwner),
		RequestPayer:         s3Types.RequestPayer(requestPayer),
		SSECustomerAlgorithm: aws.String(sseCustomerAlgorithm),
		SSECustomerKey:       aws.String(sseCustomerKey),
	}
	completeMultipartUploadOutput, err := client.CompleteMultipartUpload(context.TODO(), completeMultipartUploadInput)
	if err != nil {
		return 1, err
	}
	fmt.Fprintln(os.Stderr, "All done!")
	fmt.Fprintln(os.Stderr)

	// Print the response data from CompleteMultipartUpload as the program's standard output
	output, err := jsonMarshalSortedIndent(completeMultipartUploadOutput, "", "  ")
	if err != nil {
		return 1, err
	}
	fmt.Println(string(output))

	return 0, nil
}

//lint:file-ignore ST1005 Some errors are printed as diagnostic output and need proper punctuation
