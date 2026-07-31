package audio

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"strings"

	"nvidia-router/internal/apierror"
	"nvidia-router/internal/modelcatalog"
	"nvidia-router/internal/router"
)

// MaxAudioBodyBytes bounds the overall multipart body size (headers + file).
const MaxAudioBodyBytes = 25 << 20

// Request captures the validated parts of an ASR multipart request. Audio is
// kept in a bounded replay body; files larger than 1 MiB are streamed to a
// request-scoped 0600 temporary file.
type Request struct {
	file        *multipart.FileHeader
	fileBody    router.ReplayableBody
	form        *multipart.Form
	model       string
	language    string
	prompt      string
	granularity string
	responseFmt string
}

// ParseMultipart validates a multipart ASR request from the wire. The body is
// capped before ParseMultipartForm reads it, so oversized headers or fields are
// rejected too. ParseMultipartForm's memory threshold is deliberately small;
// the uploaded file is copied into the replay body without allocating file.Size
// bytes in memory.
func ParseMultipart(request *http.Request, tempDirs ...string) (Request, error) {
	tempDir := os.TempDir()
	if len(tempDirs) > 0 && tempDirs[0] != "" {
		tempDir = tempDirs[0]
	}
	request.Body = http.MaxBytesReader(nil, request.Body, MaxAudioBodyBytes)
	if err := request.ParseMultipartForm(1 << 20); err != nil {
		if isMaxBytesError(err) {
			param := "body"
			return Request{}, &apierror.Error{
				Status: http.StatusRequestEntityTooLarge, Type: "invalid_request_error", Code: "request_too_large",
				Message: "The multipart audio request exceeds the 25 MiB limit.", Param: &param,
			}
		}
		return Request{}, invalidRequest("invalid_audio", "file", "The multipart audio request could not be parsed.")
	}
	values := request.MultipartForm.Value
	model := strings.TrimSpace(firstValue(values["model"]))
	if model == "" {
		return Request{}, invalidRequest("missing_required_parameter", "model", "The model parameter is required.")
	}
	files := request.MultipartForm.File["file"]
	if len(files) == 0 {
		return Request{}, invalidRequest("missing_required_parameter", "file", "The file parameter is required.")
	}
	file := files[0]
	if file.Size == 0 {
		return Request{}, invalidRequest("invalid_audio", "file", "The uploaded audio file is empty.")
	}
	if file.Size > MaxAudioBodyBytes {
		return Request{}, invalidRequest("invalid_audio", "file", "The uploaded audio exceeds the 25 MiB limit.")
	}
	fp, err := file.Open()
	if err != nil {
		return Request{}, invalidRequest("invalid_audio", "file", "The uploaded audio file could not be read.")
	}
	fileBody, err := router.NewReplayableBodyFromReader(fp, file.Size, tempDir)
	_ = fp.Close()
	if err != nil {
		return Request{}, invalidRequest("invalid_audio", "file", "The uploaded audio file could not be read.")
	}
	return Request{
		file:        file,
		fileBody:    fileBody,
		form:        request.MultipartForm,
		model:       model,
		language:    firstValue(values["language"]),
		prompt:      firstValue(values["prompt"]),
		granularity: firstValue(values["timestamp_granularity"]),
		responseFmt: firstValue(values["response_format"]),
	}, nil
}

func isMaxBytesError(err error) bool {
	var maxErr *http.MaxBytesError
	return errors.As(err, &maxErr)
}

func firstValue(values []string) string {
	if len(values) > 0 {
		return strings.TrimSpace(values[0])
	}
	return ""
}

// ModelID returns the public model identifier for whitelist resolution.
func (r Request) ModelID() string { return r.model }

// Requirements declares that ASR requires a verified kind=asr model.
func (r Request) Requirements() modelcatalog.Requirements {
	return modelcatalog.Requirements{Kind: modelcatalog.KindASR}
}

// FileName reports the uploaded filename for forwarding to NVIDIA.
func (r Request) FileName() string { return r.file.Filename }

// FileBytes returns the decoded audio bytes for compatibility with small test
// callers. Production forwarding uses FileBody and does not retain this slice.
func (r Request) FileBytes() []byte {
	reader, err := r.fileBody.Open()
	if err != nil {
		return nil
	}
	defer reader.Close()
	payload, err := io.ReadAll(reader)
	if err != nil {
		return nil
	}
	return payload
}

// FileSize reports the uploaded audio size without reading it into memory.
func (r Request) FileSize() int64 { return r.fileBody.Size() }

// Close releases request-scoped replay storage.
func (r Request) Close() error {
	var firstErr error
	if r.fileBody != nil {
		firstErr = r.fileBody.Close()
	}
	if r.form != nil {
		if err := r.form.RemoveAll(); firstErr == nil && err != nil {
			firstErr = err
		}
	}
	return firstErr
}

// EncodeUpstream rebuilds a multipart body with the mapped model and forwards
// only NVIDIA-mappable fields (language/prompt/response_format and timestamp
// granularity). It returns the body bytes and the content-type header carrying
// the multipart boundary.
func (r Request) EncodeUpstream(upstreamModel string) ([]byte, string, error) {
	var body strings.Builder
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("model", upstreamModel); err != nil {
		return nil, "", fmt.Errorf("write model field: %w", err)
	}
	if r.language != "" {
		if err := writer.WriteField("language", r.language); err != nil {
			return nil, "", fmt.Errorf("write language field: %w", err)
		}
	}
	if r.prompt != "" {
		if err := writer.WriteField("prompt", r.prompt); err != nil {
			return nil, "", fmt.Errorf("write prompt field: %w", err)
		}
	}
	if r.responseFmt != "" {
		if err := writer.WriteField("response_format", r.responseFmt); err != nil {
			return nil, "", fmt.Errorf("write response_format field: %w", err)
		}
	}
	if r.granularity != "" {
		if err := writer.WriteField("timestamp_granularity", r.granularity); err != nil {
			return nil, "", fmt.Errorf("write timestamp field: %w", err)
		}
	}
	part, err := writer.CreatePart(filePartHeader(r.file.Filename, r.AudioContentType()))
	if err != nil {
		return nil, "", fmt.Errorf("create file part: %w", err)
	}
	reader, err := r.fileBody.Open()
	if err != nil {
		return nil, "", fmt.Errorf("open file body: %w", err)
	}
	if _, err := io.Copy(part, reader); err != nil {
		_ = reader.Close()
		return nil, "", fmt.Errorf("write file bytes: %w", err)
	}
	if err := reader.Close(); err != nil {
		return nil, "", fmt.Errorf("close file body: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("close multipart writer: %w", err)
	}
	return []byte(body.String()), writer.FormDataContentType(), nil
}

// AudioContentType reports a plausible content type for the upload so the
// multipart part carries a meaningful Content-Type. Unknown extensions fall
// back to application/octet-stream.
func filePartHeader(filename, contentType string) textproto.MIMEHeader {
	header := make(textproto.MIMEHeader)
	escapedFilename := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(filename)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, escapedFilename))
	header.Set("Content-Type", contentType)
	return header
}

func (r Request) AudioContentType() string {
	ct := r.file.Header.Get("Content-Type")
	if ct != "" {
		if mediaType, _, err := mime.ParseMediaType(ct); err == nil && mediaType != "" {
			return mediaType
		}
	}
	switch ext := strings.ToLower(filepathExt(r.file.Filename)); ext {
	case ".wav":
		return "audio/wav"
	case ".mp3":
		return "audio/mpeg"
	case ".flac":
		return "audio/flac"
	case ".ogg", ".oga":
		return "audio/ogg"
	case ".m4a", ".aac":
		return "audio/aac"
	default:
		return "application/octet-stream"
	}
}

func filepathExt(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i:]
	}
	return ""
}

func invalidRequest(code, param, message string) *apierror.Error {
	var parameter *string
	if param != "" {
		parameter = &param
	}
	return &apierror.Error{
		Status: 400, Type: "invalid_request_error", Code: code, Message: message, Param: parameter,
	}
}
