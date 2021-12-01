shrimp is a small program that can reliably upload large files to Amazon S3. My personal use case is to upload large files to S3 over a slow residential connection, and shrimp is optimized for this use case.

Features:
- shrimp supports most of the arguments used for `aws s3 cp`. In many cases you can simply replace `aws s3 cp` with `shrimp` and everything will work.
- shrimp has interactive keyboard controls that lets you limit the bandwidth used for the upload (you can also specify an initial limit with `-bwlimit`, e.g. `-bwlimit=2.5m` for 2.5 MB/s). While the upload is in progress, press <kbd>?</kbd> to see the available keyboard controls.
- shrimp can resume the upload in case it fails for whatever reason (just re-run the command). Unlike the aws cli, shrimp will never abort the multipart upload in case of failures ([please set up a lifecycle policy for this!](https://aws.amazon.com/blogs/aws-cloud-financial-management/discovering-and-deleting-incomplete-multipart-uploads-to-lower-amazon-s3-costs/)).
- shrimp supports automatically attaching SHA256 checksums to the object metadata if a `SHA256SUMS` file present in the working directory. You can use [s3sha256sum](https://github.com/stefansundin/s3sha256sum) to verify the object after it has been uploaded. [See here for more information.](https://github.com/stefansundin/s3sha256sum/discussions/1)

TODO:
- Automatically change bandwidth limit on a schedule.

Keep in mind:
- shrimp will always use a multipart upload, so do not use it for small files.
- shrimp uploads a single part at a time.

Current status: **testing phase**. Please do not use it for important files just yet. Please report any bugs.

## Installation

Precompiled binaries will be provided at a later date. For now you can install using `go install`:

```
go install github.com/stefansundin/shrimp@latest
```

## Usage

```
$ shrimp -help
Usage: shrimp [parameters] <LocalPath> <S3Uri>
LocalPath must be a local file.
S3Uri must have the format s3://<bucketname>/<key>.

Parameters:
  -bwlimit string
    	Bandwidth limit. (e.g. "2.5m")
  -ca-bundle string
    	The CA certificate bundle to use when verifying SSL certificates.
  -cache-control string
    	Specifies caching behavior for the object.
  -content-disposition string
    	Specifies presentational information for the object.
  -content-encoding string
    	Specifies what content encodings have been applied to the object.
  -content-language string
    	Specifies the language the content is in.
  -content-type string
    	A standard MIME type describing the format of the object data.
  -debug
    	Turn on debug logging.
  -endpoint-url string
    	Override the S3 endpoint URL. (for use with S3 compatible APIs)
  -expected-bucket-owner string
    	The account ID of the expected bucket owner.
  -metadata string
    	A map of metadata to store with the object in S3. (JSON syntax is not supported)
  -mfa-duration duration
    	MFA duration. shrimp will prompt for another code after this duration. (max "12h") (default 1h0m0s)
  -mfa-secret
    	Provide the MFA secret and shrimp will automatically generate TOTP codes. (useful if the upload takes longer than the allowed assume role duration)
  -no-sign-request
    	Do not sign requests. This does not work if used with AWS, but may work with other S3 APIs.
  -no-verify-ssl
    	Do not verify SSL certificates.
  -part-size string
    	Override automatic part size. (e.g. "128m")
  -profile string
    	Use a specific profile from your credential file.
  -storage-class string
    	Storage class. Known values: STANDARD, REDUCED_REDUNDANCY, STANDARD_IA, ONEZONE_IA, INTELLIGENT_TIERING, GLACIER, DEEP_ARCHIVE, OUTPOSTS.
  -tagging string
    	The tag-set for the object. The tag-set must be encoded as URL Query parameters.
  -version
    	Print version number.
```
