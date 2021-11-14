Based on https://github.com/mxk/go-flowrate.

Modified to allow seeking and to calculate a total progress.

Optionally, it can also skip rate limiting on the first pass. This is necessary when transferring files over unencrypted HTTP, since the AWS SDK will in this case calculate a checksum of the payload before it initiates the request. This case will only happen when the user uses `-endpoint-url` with an `http://` endpoint.

Here's a list of the calls that will occur:

1. The AWS SDK first checks if the stream is seekable, and seeks to the current position to get the start position (which will always be position 0 for shrimp). https://github.com/aws/smithy-go/blob/ec5b67b07969f689d60b7773c2bef00cc047cd7e/transport/http/request.go#L113
2. It then seeks to the end to calculate the content length. https://github.com/aws/smithy-go/blob/ec5b67b07969f689d60b7773c2bef00cc047cd7e/transport/http/request.go#L61
3. It then seeks to the beginning. https://github.com/aws/smithy-go/blob/ec5b67b07969f689d60b7773c2bef00cc047cd7e/transport/http/request.go#L72
4. It then reads the stream and calculates the checksum of the data. https://github.com/aws/aws-sdk-go-v2/blob/af489c33fe60dd0b21b8ab9ffc700b9204dd4bab/aws/signer/v4/middleware.go#L138-L152
5. It then seeks to the beginning again. https://github.com/aws/smithy-go/blob/ec5b67b07969f689d60b7773c2bef00cc047cd7e/transport/http/request.go#L91
6. It then reads the data and sends it over the network as the request payload.

In summary the following calls are made:

```go
streamStartPos = stream.Seek(0, io.SeekCurrent)
endOffset = stream.Seek(0, io.SeekEnd)
stream.Seek(streamStartPos, io.SeekStart)
io.Copy(hash, stream)
stream.Seek(streamStartPos, io.SeekStart)
// the data then is read again and sent over the network, we only rate limit starting here
```
