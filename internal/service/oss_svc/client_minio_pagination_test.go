package oss_svc

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/stretchr/testify/require"
)

// TestMinioAdapterPreservesAnUnderfilledTruncatedPage reproduces the real
// minio-go boundary: it emits every page's Contents before CommonPrefixes. If
// an underfilled truncated page makes the iterator cross into the next S3 page,
// stopping after maxKeys+1 flattened items can cut that second page before its
// lower-sorting CommonPrefix and make a StartAfter cursor skip the subtree.
func TestMinioAdapterPreservesAnUnderfilledTruncatedPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		q := r.URL.Query()
		switch {
		case q.Get("continuation-token") == "page-2":
			writeListV2Page(w, "bucket", false, "", contentKeys("c", 201), []string{"b-prefix/"})
		case q.Get("start-after") != "":
			writeListV2Page(w, "bucket", false, "", []string{"c200", "c201"}, nil)
		default:
			// Deliberately fewer than MaxKeys while still truncated.
			writeListV2Page(w, "bucket", true, "page-2", []string{"a001", "a002"}, nil)
		}
	}))
	defer server.Close()

	endpoint, err := url.Parse(server.URL)
	require.NoError(t, err)
	mc, err := minio.New(endpoint.Host, &minio.Options{
		Creds:  credentials.NewStaticV4("access", "secret", ""),
		Secure: false,
		Region: "us-east-1",
	})
	require.NoError(t, err)

	client := newMinioAdapter(mc)
	token := ""
	var prefixes []string
	for {
		res, err := ListObjectsWith(context.Background(), client, "bucket", "", 200, token)
		require.NoError(t, err)
		prefixes = append(prefixes, res.Prefixes...)
		if !res.IsTruncated {
			break
		}
		token = res.NextContinuationToken
	}
	require.Contains(t, prefixes, "b-prefix/")
}

func TestMinioAdapterListObjectsReturnsPromptlyWhenContextIsCanceled(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	requestCanceled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-r.Context().Done():
			close(requestCanceled)
		case <-release:
		}
	}))
	defer func() {
		close(release)
		server.Close()
	}()

	endpoint, err := url.Parse(server.URL)
	require.NoError(t, err)
	mc, err := minio.New(endpoint.Host, &minio.Options{
		Creds: credentials.NewStaticV4("access", "secret", ""), Secure: false, Region: "us-east-1",
	})
	require.NoError(t, err)
	client := newMinioAdapter(mc)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, callErr := client.ListObjects(ctx, "bucket", "", 100, "")
		done <- callErr
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("ListObjects stayed blocked after context cancellation")
	}
	select {
	case <-requestCanceled:
	case <-time.After(time.Second):
		t.Fatal("the underlying ListObjects HTTP request was not canceled")
	}
}

func contentKeys(prefix string, n int) []string {
	keys := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		keys = append(keys, fmt.Sprintf("%s%03d", prefix, i))
	}
	return keys
}

func writeListV2Page(
	w http.ResponseWriter, bucket string, truncated bool, nextToken string, contents, prefixes []string,
) {
	var body strings.Builder
	body.WriteString(`<?xml version="1.0" encoding="UTF-8"?><ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">`)
	fmt.Fprintf(&body, "<Name>%s</Name><KeyCount>%d</KeyCount><MaxKeys>201</MaxKeys><IsTruncated>%t</IsTruncated>",
		bucket, len(contents)+len(prefixes), truncated)
	if nextToken != "" {
		fmt.Fprintf(&body, "<NextContinuationToken>%s</NextContinuationToken>", nextToken)
	}
	for _, key := range contents {
		fmt.Fprintf(&body, "<Contents><Key>%s</Key><Size>1</Size></Contents>", key)
	}
	for _, prefix := range prefixes {
		fmt.Fprintf(&body, "<CommonPrefixes><Prefix>%s</Prefix></CommonPrefixes>", prefix)
	}
	body.WriteString("</ListBucketResult>")
	_, _ = w.Write([]byte(body.String()))
}

// TestMinioAdapterDecodesURLEncodedListing 复现 Cloudflare R2 上的真实缺陷:请求带了
// encoding-type=url,R2 连 "/" 也编码成 %2F(AWS / MinIO 不编码 "/",所以那边只有带空格或
// 非 ASCII 的 key 受影响)。不解码的话,文件夹显示成 "users%2F",点进去是按字面
// "users%2F" 当前缀再列一次,永远是空的。
func TestMinioAdapterDecodesURLEncodedListing(t *testing.T) {
	list := func(t *testing.T, encodingType, key, prefix string) *ListObjectsResult {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "url", r.URL.Query().Get("encoding-type"))
			w.Header().Set("Content-Type", "application/xml")
			_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>`+
				`<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Name>bucket</Name>%s`+
				`<IsTruncated>false</IsTruncated><Contents><Key>%s</Key><Size>1</Size></Contents>`+
				`<CommonPrefixes><Prefix>%s</Prefix></CommonPrefixes></ListBucketResult>`, encodingType, key, prefix)
		}))
		defer server.Close()
		endpoint, err := url.Parse(server.URL)
		require.NoError(t, err)
		mc, err := minio.New(endpoint.Host, &minio.Options{
			Creds: credentials.NewStaticV4("access", "secret", ""), Secure: false, Region: "us-east-1",
		})
		require.NoError(t, err)
		res, err := ListObjectsWith(context.Background(), newMinioAdapter(mc), "bucket", "", 200, "")
		require.NoError(t, err)
		return res
	}

	t.Run("响应声明 EncodingType=url 时解码 key 与前缀", func(t *testing.T) {
		res := list(t, "<EncodingType>url</EncodingType>", "%E6%8A%A5%E5%91%8A+2026%2B.txt", "users%2F")
		require.Equal(t, []string{"users/"}, res.Prefixes)
		require.Len(t, res.Objects, 1)
		require.Equal(t, "报告 2026+.txt", res.Objects[0].Key)
	})

	t.Run("响应未声明编码时 key 按字面保留", func(t *testing.T) {
		res := list(t, "", "100%25.txt", "a%2Fb/")
		require.Equal(t, []string{"a%2Fb/"}, res.Prefixes)
		require.Len(t, res.Objects, 1)
		require.Equal(t, "100%25.txt", res.Objects[0].Key)
	})
}
