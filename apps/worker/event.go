package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

var errNotAnUpload = errors.New("not an S3 upload notification")

type s3Event struct {
	Records []struct {
		S3 struct {
			Bucket struct {
				Name string `json:"name"`
			} `json:"bucket"`
			Object struct {
				Key  string `json:"key"`
				Size int64  `json:"size"`
			} `json:"object"`
		} `json:"s3"`
	} `json:"Records"`
}

type uploadedObject struct {
	VideoID uuid.UUID
	Bucket  string
	Key     string
	Size    int64
}

const rawPrefix = "raw/"

func parseUpload(body string) (uploadedObject, error) {
	var event s3Event
	if err := json.Unmarshal([]byte(body), &event); err != nil {
		return uploadedObject{}, fmt.Errorf("%w: %v", errNotAnUpload, err)
	}
	if len(event.Records) == 0 {
		return uploadedObject{}, fmt.Errorf("%w: no records", errNotAnUpload)
	}

	if len(event.Records) > 1 {
		log.Printf("event carries %d records; processing only the first (key %q)",
			len(event.Records), event.Records[0].S3.Object.Key)
	}

	record := event.Records[0]

	key, err := url.QueryUnescape(record.S3.Object.Key)
	if err != nil {
		return uploadedObject{}, fmt.Errorf("%w: undecodable key %q", errNotAnUpload, record.S3.Object.Key)
	}

	rest, found := strings.CutPrefix(key, rawPrefix)
	if !found || strings.Contains(rest, "/") {
		return uploadedObject{}, fmt.Errorf("%w: key %q is not %s{uuid}", errNotAnUpload, key, rawPrefix)
	}

	id, err := uuid.Parse(rest)
	if err != nil {
		return uploadedObject{}, fmt.Errorf("%w: key %q has no video id", errNotAnUpload, key)
	}

	return uploadedObject{
		VideoID: id,
		Bucket:  record.S3.Bucket.Name,
		Key:     key,
		Size:    record.S3.Object.Size,
	}, nil
}
