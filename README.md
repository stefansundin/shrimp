shrimp is a small program that can reliably upload large files to Amazon S3.

Features:
- shrimp supports most of the arguments used for `aws s3 cp`. In many cases you can simply replace `aws s3 cp` with `shrimp` and everything will work.
- shrimp has interactive keyboard controls that lets you limit the bandwidth used for the upload (you can specify an initial limit with `--bwlimit`, e.g. `--bwlimit=2.5m` for 2.5 MB/s). While the upload is in progress, press <kbd>?</kbd> to see the available keyboard controls.
- shrimp can automatically adjust the bandwidth limit based on a schedule. [See here for more information.](https://github.com/stefansundin/s3sha256sum/discussions/4)
- shrimp can resume the upload in case it fails for whatever reason (just re-run the command). Unlike the aws cli, shrimp will never abort the multipart upload in case of failures ([please set up a lifecycle policy for this!](https://aws.amazon.com/blogs/aws-cloud-financial-management/discovering-and-deleting-incomplete-multipart-uploads-to-lower-amazon-s3-costs/)).
- shrimp supports the [Additional Checksum Algorithms feature released in February 2022](https://aws.amazon.com/blogs/aws/new-additional-checksum-algorithms-for-amazon-s3/). Use `--checksum-algorithm` to allow verification of the object without the need to download it, e.g. using [s3verify](https://github.com/stefansundin/s3verify).
- shrimp also supports automatically attaching a SHA256 checksum to the object metadata if a `SHA256SUMS` file is present in the working directory. Use `--compute-checksum` if you want shrimp to calculate the checksum and add it to the `SHA256SUMS` file. You can use [s3sha256sum](https://github.com/stefansundin/s3sha256sum) to verify the object after it has been uploaded. The `--checksum-algorithm` feature somewhat supercedes this, but there are still uses for this checksum, especially for multi-part objects. [See here for more information.](https://github.com/stefansundin/s3sha256sum/discussions/1)

Keep in mind:
- shrimp will always use a multipart upload, so do not use it for small files.
- shrimp uploads a single part at a time.

I have used the program to upload several terabytes to Amazon S3 and I consider it stable and ready for use. Please give it a try and report any issues you may encounter.

## Installation

You can download precompiled binaries [from the releases section](https://github.com/stefansundin/shrimp/releases/latest).

If you prefer to compile from source (or to use unreleased features), you can install using `go install`:

```
go install github.com/stefansundin/shrimp@latest
```

## Usage

```
$ shrimp --help
Usage: shrimp [parameters] <LocalPath> <S3Uri>
LocalPath must be a local file.
S3Uri must have the format s3://<bucketname>/<key>.

Parameters:
      --bucket-key-enabled                     Enables use of an S3 Bucket Key for object encryption with server-side encryption using AWS KMS (SSE-KMS).
      --bwlimit string                         Bandwidth limit. (e.g. "2.5m")
      --ca-bundle string                       The CA certificate bundle to use when verifying SSL certificates.
      --cache-control string                   Specifies caching behavior for the object.
      --checksum-algorithm string              The checksum algorithm to use for the object. Supported values: CRC32, CRC32C, SHA1, SHA256.
      --compute-checksum                       Compute checksum and add to SHA256SUMS file.
      --content-disposition string             Specifies presentational information for the object.
      --content-encoding string                Specifies what content encodings have been applied to the object.
      --content-language string                Specifies the language the content is in.
      --content-type string                    A standard MIME type describing the format of the object data.
      --debug                                  Turn on debug logging.
      --dryrun                                 Checks if the upload was started previously and how much was completed. (use in combination with --bwlimit to calculate remaining time)
      --endpoint-url string                    Override the S3 endpoint URL. (for use with S3 compatible APIs)
      --expected-bucket-owner string           The account ID of the expected bucket owner.
      --force                                  Overwrite existing object.
      --metadata string                        A map of metadata to store with the object in S3. (JSON syntax is not supported)
      --mfa-duration duration                  MFA duration. shrimp will prompt for another code after this duration. (max "12h") (default 1h0m0s)
      --mfa-secret                             Provide the MFA secret and shrimp will automatically generate TOTP codes. (useful if the upload takes longer than the allowed assume role duration)
      --no-sign-request                        Do not sign requests. This does not work with Amazon S3, but may work with other S3 APIs.
      --no-verify-ssl                          Do not verify SSL certificates.
      --object-lock-legal-hold-status string   Specifies whether a legal hold will be applied to this object. Possible values: ON, OFF.
      --object-lock-mode string                The Object Lock mode that you want to apply to this object. Possible values: GOVERNANCE, COMPLIANCE.
      --object-lock-retain-until-date string   The date and time when you want this object's Object Lock to expire. Must be formatted as a timestamp parameter. (e.g. "2022-03-14T15:14:15Z")
      --part-size string                       Override automatic part size. (e.g. "128m")
      --profile string                         Use a specific profile from your credential file.
      --region string                          The bucket region. Avoids one API call.
      --request-payer string                   Confirms that the requester knows that they will be charged for the requests. Possible values: requester.
      --schedule string                        Schedule file to use for automatically adjusting the bandwidth limit (see https://github.com/stefansundin/shrimp/discussions/4).
      --sse string                             Specifies server-side encryption of the object in S3. Possible values: AES256, aws:kms, aws:kms:dsse.
      --sse-c string                           Specifies server-side encryption using customer provided keys of the the object in S3. AES256 is the only valid value. If you provide this value, --sse-c-key must be specified as well.
      --sse-c-key string                       The customer-provided encryption key to use to server-side encrypt the object in S3. The key provided should not be base64 encoded.
      --sse-kms-key-id string                  The customer-managed AWS Key Management Service (KMS) key ID that should be used to server-side encrypt the object in S3.
      --storage-class string                   Storage class. Known values: STANDARD, REDUCED_REDUNDANCY, STANDARD_IA, ONEZONE_IA, INTELLIGENT_TIERING, GLACIER, DEEP_ARCHIVE, OUTPOSTS, GLACIER_IR, SNOW.
      --tagging string                         The tag-set for the object. The tag-set must be encoded as URL Query parameters.
      --use-accelerate-endpoint                Use S3 Transfer Acceleration.
      --use-path-style                         Use S3 Path Style.
      --version                                Print version number.
```

To use S3 dual-stack endpoints, configure the environment variable `AWS_USE_DUALSTACK_ENDPOINT=true`.
