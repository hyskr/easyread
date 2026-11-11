package chat

import (
	"context"
	"encoding/base64"
	"testing"
)

func TestDocumentAttachmentText(t *testing.T) {
	docs, err := extractDocuments(context.Background(), []any{M{"name": "notes.md", "data": base64.StdEncoding.EncodeToString([]byte("# Test\n正文"))}})
	if err != nil || len(docs) != 1 || obj(docs[0])["text"] != "# Test\n正文" {
		t.Fatal(docs, err)
	}
}
func TestDocumentAttachmentRejectsEmpty(t *testing.T) {
	if _, err := extractDocuments(context.Background(), []any{M{"name": "empty.txt", "data": ""}}); err == nil {
		t.Fatal("empty accepted")
	}
}
