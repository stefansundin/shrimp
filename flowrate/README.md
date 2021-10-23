Based on https://github.com/mxk/go-flowrate.

Modified to allow seeking and to calculate a total progress.

The AWS SDK used to calculate checksum on the payload, but no longer does if the connection is over TLS ([PR](https://github.com/aws/aws-sdk-go-v2/pull/1354)). This package used to have code that skipped ratelimiting on the first pass (check the git history).
