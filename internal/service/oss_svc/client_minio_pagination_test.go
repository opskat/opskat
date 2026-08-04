package oss_svc

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

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
