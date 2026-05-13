package media

import (
	"context"
	"io"
    "fmt"

	"go.mau.fi/whatsmeow"
)

type Uploader struct {
	client *whatsmeow.Client
}

func NewUploader(client *whatsmeow.Client) *Uploader {
	return &Uploader{client: client}
}

func (u *Uploader) Upload(ctx context.Context, data []byte, appType whatsmeow.MediaType) (whatsmeow.UploadResponse, error) {
    if u.client == nil {
        return whatsmeow.UploadResponse{}, fmt.Errorf("client is nil")
    }
    
    // Check if connected
    if !u.client.IsConnected() {
         return whatsmeow.UploadResponse{}, fmt.Errorf("client is not connected")
    }

	return u.client.Upload(ctx, data, appType)
}

func (u *Uploader) UploadStream(ctx context.Context, r io.Reader, appType whatsmeow.MediaType) (whatsmeow.UploadResponse, error) {
    // Read all for now, as whatsmeow Upload takes []byte usually or stream depending on method
    // whatsmeow.Upload usually takes []byte. 
    data, err := io.ReadAll(r)
    if err != nil {
        return whatsmeow.UploadResponse{}, err
    }
    return u.Upload(ctx, data, appType)
}
