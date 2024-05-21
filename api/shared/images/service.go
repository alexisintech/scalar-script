package images

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"

	"clerk/api/apierror"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/constants"
	"clerk/pkg/limitreader"
	"clerk/pkg/rand"
	"clerk/pkg/storage"
	"clerk/repository"
	"clerk/utils/database"

	"github.com/volatiletech/null/v8"
)

type Service struct {
	storage storage.ReadWriter

	// repositories
	imageRepo *repository.Images
}

func NewService(storageClient storage.ReadWriter) *Service {
	return &Service{
		storage:   storageClient,
		imageRepo: repository.NewImages(),
	}
}

var imgTypesRe = regexp.MustCompile(`^image/(jpeg|png|gif|webp|x-icon|vnd\.microsoft\.icon)$`)

const (
	PrefixUploaded = "uploaded"
)

type ImageParams struct {
	Filename           string
	Prefix             string
	Src                io.ReadCloser
	UploaderUserID     string
	UsedByResourceType *string
	ImageID            string
}

const maxImageSize = 10_000_000

func (s *Service) PublicURL(prefix, imageID string) (string, error) {
	return s.storage.PublicURL(uploadPath(imageID, prefix))
}

// Create uploads an image and creates an image record in the database.
// Returns the newly created image.
func (s *Service) Create(
	ctx context.Context,
	exec database.Executor,
	params ImageParams,
) (*model.Image, apierror.Error) {
	header := bytes.NewBuffer(nil)
	_, err := io.CopyN(header, params.Src, 512)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, apierror.Unexpected(err)
	}

	fileType := http.DetectContentType(header.Bytes())
	if !imgTypesRe.MatchString(fileType) {
		return nil, apierror.ImageTypeNotSupported(fileType)
	}

	if params.ImageID == "" {
		imageID := rand.InternalClerkID(constants.IDPImage)
		params.ImageID = imageID
	}

	path := uploadPath(params.ImageID, params.Prefix)
	publicURL, err := s.storage.PublicURL(path)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	var netErr net.Error
	size, err := s.uploadImage(ctx, path, header, limitreader.NewLimitStreamReadCloser(params.Src, maxImageSize))
	if errors.Is(err, limitreader.ErrThresholdExceeded) {
		return nil, apierror.ImageTooLarge()
	} else if errors.As(err, &netErr) && netErr.Timeout() {
		return nil, apierror.GatewayTimeout()
	} else if err != nil {
		return nil, apierror.Unexpected(err)
	} else if size == 0 {
		return nil, apierror.RequestWithoutImage()
	}
	image := &model.Image{Image: &sqbmodel.Image{
		ID:                 params.ImageID,
		Name:               params.Filename,
		PublicURL:          publicURL,
		FileType:           fileType,
		Bytes:              size,
		UploaderUserID:     null.StringFrom(params.UploaderUserID),
		UsedByResourceType: null.StringFromPtr(params.UsedByResourceType),
	}}

	err = s.imageRepo.Insert(ctx, exec, image)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return image, nil
}

func (s *Service) uploadImage(ctx context.Context, uploadPath string, header *bytes.Buffer, src io.ReadCloser) (int, error) {
	size, err := s.storage.Write(ctx, uploadPath, io.MultiReader(header, src))
	if err != nil {
		return 0, err
	}
	if err := src.Close(); err != nil {
		return 0, err
	}
	return int(size), nil
}

func uploadPath(imageID, prefix string) string {
	return fmt.Sprintf("%s/%s", prefix, imageID)
}

const (
	headerMultipart   = "multipart/form-data"
	headerOctetStream = "application/octet-stream"

	maxFileSize = 10 * 1024 * 1024 // 10MB
)

func ReadFileOrBase64(r *http.Request) (io.ReadCloser, apierror.Error) {
	switch contentType := r.Header.Get("content-type"); {
	case strings.HasPrefix(contentType, headerOctetStream):
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
		defer r.Body.Close()
		if len(body) == 0 {
			return nil, apierror.RequestWithoutImage()
		}

		// There's two parts in a base64 encoded data URL.
		// Second part is the actual data.
		parts := strings.Split(string(body), ",")
		if len(parts) != 2 {
			return nil, apierror.FormInvalidParameterFormat("file", "must be a valid base64 encoded image")
		}
		decoded, err := base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			return nil, apierror.FormInvalidParameterFormat("file", "must be a valid base64 encoded image")
		}
		if int64(len(decoded)) > maxFileSize {
			return nil, apierror.InvalidRequestBody(fmt.Errorf("cannot be larger than %d bytes", maxFileSize))
		}
		return io.NopCloser(bytes.NewReader(decoded)), nil
	case strings.HasPrefix(contentType, headerMultipart):
		err := r.ParseMultipartForm(maxFileSize)
		if err != nil {
			return nil, apierror.InvalidRequestBody(err)
		}

		file, _, err := r.FormFile("file")
		if err != nil {
			return nil, apierror.InvalidRequestBody(err)
		}
		if file == nil {
			return nil, apierror.RequestWithoutImage()
		}
		return file, nil
	default:
		return nil, apierror.UnsupportedContentType(contentType, strings.Join([]string{headerOctetStream, headerMultipart}, ","))
	}
}

// Find Provider using {provider}/{imageID} of imageURL
func ExtractPrefixFromImageURL(imageURL string) (string, apierror.Error) {
	imageURLParts := strings.Split(imageURL, "/")

	// example: https://<bucket>/<oauth provider>/<image ID>
	if len(imageURLParts) != 5 {
		return "", apierror.ImageNotFound()
	}

	return imageURLParts[len(imageURLParts)-2], nil
}

// Find Provider using {provider}/{imageID} of imageURL
func ExtractImageIDFromImageURL(imageURL string) (string, apierror.Error) {
	imageURLParts := strings.Split(imageURL, "/")

	// example: https://<bucket>/<oauth provider>/<image ID>
	if len(imageURLParts) != 5 {
		return "", apierror.ImageNotFound()
	}

	return imageURLParts[len(imageURLParts)-1], nil
}
