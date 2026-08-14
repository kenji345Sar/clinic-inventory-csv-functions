package storage

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3Object はS3上のオブジェクト1件のメタ情報。
// ETagはオブジェクトの内容から決まるため、同じキーでも中身が更新されれば変わる。
// 取り込み済み判定(キー+ETagが一致すればスキップ)に使う。
type S3Object struct {
	Key          string
	ETag         string
	Size         int64
	LastModified time.Time
}

// S3Reader はS3からのオブジェクト一覧取得・ダウンロードだけを担う薄いラッパー。
// 卸から届く商品マスタCSVのように「こちらが読む」側の処理で使う
// (こちらが書く側は S3Uploader)。
type S3Reader struct {
	client *s3.Client
	bucket string
}

func NewS3Reader(client *s3.Client, bucket string) *S3Reader {
	return &S3Reader{client: client, bucket: bucket}
}

// List はprefix配下のオブジェクトを全件返す。1リクエストで返るのは最大1000件のため、
// 継続トークンがある間ページングして読み切る。
func (r *S3Reader) List(ctx context.Context, prefix string) ([]S3Object, error) {
	var objects []S3Object
	var token *string
	for {
		out, err := r.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            &r.bucket,
			Prefix:            &prefix,
			ContinuationToken: token,
		})
		if err != nil {
			return nil, fmt.Errorf("S3の一覧取得に失敗しました(prefix=%s): %w", prefix, err)
		}
		for _, o := range out.Contents {
			obj := S3Object{}
			if o.Key != nil {
				obj.Key = *o.Key
			}
			if o.ETag != nil {
				obj.ETag = *o.ETag
			}
			if o.Size != nil {
				obj.Size = *o.Size
			}
			if o.LastModified != nil {
				obj.LastModified = *o.LastModified
			}
			objects = append(objects, obj)
		}
		if out.IsTruncated == nil || !*out.IsTruncated {
			return objects, nil
		}
		token = out.NextContinuationToken
	}
}

// Get はkeyのオブジェクトを丸ごと読み込む。商品マスタCSVは大きくても数MB想定のため
// ストリーム処理はせず全体をメモリに載せる。
func (r *S3Reader) Get(ctx context.Context, key string) ([]byte, error) {
	out, err := r.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &r.bucket,
		Key:    &key,
	})
	if err != nil {
		return nil, fmt.Errorf("S3からの取得に失敗しました(key=%s): %w", key, err)
	}
	defer out.Body.Close()

	body, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, fmt.Errorf("S3オブジェクトの読み込みに失敗しました(key=%s): %w", key, err)
	}
	return body, nil
}
