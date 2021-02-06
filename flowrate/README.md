Based on https://github.com/mxk/go-flowrate.

Modified to allow seeking.

Skips rate limiting on the first pass since that's when the AWS SDK calculates the checksum:

1. The AWS SDK first checks if the stream is seekable, and seeks to the current position to get the start position (which will be 0 for us). https://github.com/aws/smithy-go/blob/ec5b67b07969f689d60b7773c2bef00cc047cd7e/transport/http/request.go#L113
2. It then seeks to the end to calculate the content length. https://github.com/aws/smithy-go/blob/ec5b67b07969f689d60b7773c2bef00cc047cd7e/transport/http/request.go#L61
3. It then seeks to the beginning. https://github.com/aws/smithy-go/blob/ec5b67b07969f689d60b7773c2bef00cc047cd7e/transport/http/request.go#L72
4. It then reads the stream and calculates the checksum of the data. https://github.com/aws/aws-sdk-go-v2/blob/af489c33fe60dd0b21b8ab9ffc700b9204dd4bab/aws/signer/v4/middleware.go#L138-L152
5. It then seeks to the beginning again. https://github.com/aws/smithy-go/blob/ec5b67b07969f689d60b7773c2bef00cc047cd7e/transport/http/request.go#L91
6. It then reads the data for the request itself.

In summary the following calls are made:

```go
streamStartPos = stream.Seek(0, io.SeekCurrent)
endOffset = stream.Seek(0, io.SeekEnd)
stream.Seek(streamStartPos, io.SeekStart)
io.Copy(hash, stream)
stream.Seek(streamStartPos, io.SeekStart)
// data is read and sent, we only rate limit starting here
```
