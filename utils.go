package main

import (
	"bufio"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	s3Types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

const kiB = 1024
const MiB = 1024 * kiB
const GiB = 1024 * MiB
const TiB = 1024 * GiB

func min(a, b int64) int64 {
	if a > b {
		return b
	}
	return a
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func niceDuration(d time.Duration) time.Duration {
	if d < time.Second {
		return d.Round(time.Millisecond)
	}
	return d.Round(time.Second)
}

func isNumeric(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func parseS3Uri(s string) (string, string) {
	if !strings.HasPrefix(s, "s3://") {
		return "", ""
	}
	parts := strings.SplitN(s[5:], "/", 2)
	if len(parts) == 0 {
		return "", ""
	} else if len(parts) == 1 {
		return parts[0], ""
	} else {
		return parts[0], parts[1]
	}
}

func parseRate(s string) (int64, error) {
	if s == "unlimited" {
		return 0, nil
	}

	factor := 1
	suffix := s[len(s)-1]
	if suffix == 'k' || suffix == 'K' {
		factor = 1e3
	} else if suffix == 'm' || suffix == 'M' {
		factor = 1e6
	} else if suffix == 'g' || suffix == 'G' {
		// If you have any use of this then you are lucky and I am jealous :)
		factor = 1e9
	}
	if factor != 1 {
		s = s[0 : len(s)-1]
	}

	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	return int64(math.Round(f * float64(factor))), nil
}

func parseFilesize(s string) (int64, error) {
	factor := 1
	suffix := s[len(s)-1]
	if suffix == 'k' || suffix == 'K' {
		factor = kiB
	} else if suffix == 'm' || suffix == 'M' {
		factor = MiB
	} else if suffix == 'g' || suffix == 'G' {
		factor = GiB
	}
	if factor != 1 {
		s = s[0 : len(s)-1]
	}

	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	return int64(math.Round(f * float64(factor))), nil
}

func parseMetadata(s string) (map[string]string, error) {
	m := make(map[string]string)
	for _, kv := range strings.Split(s, ",") {
		e := strings.SplitN(kv, "=", 2)
		if len(e) < 2 {
			return nil, errors.New("Malformed metadata")
		}
		key := e[0]
		value := e[1]
		m[key] = value
	}
	return m, nil
}

func formatSize(size int64) string {
	if size < 1e3 {
		return fmt.Sprintf("%d bytes", size)
	} else if size < 1e6 {
		return fmt.Sprintf("%.1f kB", float64(size)/1e3)
	} else if size < 1e9 {
		return fmt.Sprintf("%.1f MB", float64(size)/1e6)
	} else if size < 1e12 {
		return fmt.Sprintf("%.1f GB", float64(size)/1e9)
	} else {
		return fmt.Sprintf("%.1f TB", float64(size)/1e12)
	}
}

// The S3 docs state GB and TB but they actually mean GiB and TiB
// For consistency, format filesizes in GiB and TiB
func formatFilesize(size int64) string {
	if size < kiB {
		return fmt.Sprintf("%d bytes", size)
	} else if size < MiB {
		return fmt.Sprintf("%.1f kiB (%d bytes)", float64(size)/float64(kiB), size)
	} else if size < GiB {
		return fmt.Sprintf("%.1f MiB (%d bytes)", float64(size)/float64(MiB), size)
	} else if size < TiB {
		return fmt.Sprintf("%.1f GiB (%d bytes)", float64(size)/float64(GiB), size)
	} else {
		return fmt.Sprintf("%.1f TiB (%d bytes)", float64(size)/float64(TiB), size)
	}
}

func formatLimit(rate int64, parenthesis bool) string {
	if rate == 0 {
		return ""
	}
	if parenthesis {
		return fmt.Sprintf(" (limit: %s/s)", formatSize(rate))
	}
	return fmt.Sprintf(", limit: %s/s", formatSize(rate))
}

func formatLimit2(rate int64) string {
	if rate == 0 {
		return "unlimited"
	}
	return fmt.Sprintf("%s/s", formatSize(rate))
}

func lookupChecksum(sumsFn string, fn string) (string, error) {
	entryPath, err := filepath.Abs(fn)
	if err != nil {
		return "", err
	}

	file, err := os.Open(sumsFn)
	if err != nil {
		return "", err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		sum := line[0:64]
		mid := line[64:66]
		if mid != "  " && mid != " *" {
			return "", errors.New("Unsupported SHA256SUMS format")
		}
		path, err := filepath.Abs(line[66:])
		if err != nil {
			return "", err
		}
		if path == entryPath {
			return sum, nil
		}
	}

	if err := scanner.Err(); err != nil {
		return "", err
	}

	return "", nil
}

func computeSha256Sum(fn string) (string, error) {
	file, err := os.Open(fn)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	_, err = io.Copy(hash, file)
	if err != nil {
		return "", err
	}
	sum := hex.EncodeToString(hash.Sum(nil))
	return sum, nil
}

func knownStorageClasses() []string {
	values := s3Types.StorageClassStandard.Values()
	result := make([]string, len(values))
	for i, v := range values {
		result[i] = string(v)
	}
	return result
}

// https://github.com/aws/aws-sdk-go/blob/e2d6cb448883e4f4fcc5246650f89bde349041ec/service/s3/bucket_location.go#L15-L32
// Would be nice if aws-sdk-go-v2 supported this.
func normalizeBucketLocation(loc s3Types.BucketLocationConstraint) string {
	if loc == "" {
		return "us-east-1"
	}
	return string(loc)
}

func isSmithyErrorCode(err error, code int) bool {
	var re *smithyhttp.ResponseError
	if errors.As(err, &re) && re.HTTPStatusCode() == code {
		return true
	}
	return false
}

func generateOTP(secretBytes []byte, counter uint64, hashAlg func() hash.Hash, digits int) (string, error) {
	mac := hmac.New(hashAlg, secretBytes)
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, counter)
	_, err := mac.Write(buf)
	if err != nil {
		return "", err
	}
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0xf
	value := int64(((int(sum[offset]) & 0x7f) << 24) |
		((int(sum[offset+1] & 0xff)) << 16) |
		((int(sum[offset+2] & 0xff)) << 8) |
		(int(sum[offset+3]) & 0xff))
	mod := int32(value % int64(math.Pow10(digits)))
	return fmt.Sprintf(fmt.Sprintf("%%0%dd", digits), mod), nil
}
