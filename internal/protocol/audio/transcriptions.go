package audio

import (
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"

	"nvidia-router/internal/apierror"
	"nvidia-router/internal/modelcatalog"
)

// MaxAudioBodyBytes bounds the overall multipart body size (headers + file).
const MaxAudioBodyBytes = 25 << 20

// Request captures the validated parts of an ASR multipart request. The decoded
// file bytes are held in a replay-capable buffer, not in any persistent field.
type Request struct {
	file        *multipart.FileHeader
	fileBytes   []byte
	model       string
	language    string
	prompt      string
	granularity string
	responseFmt string
}

// ParseMultipart validates a multipart ASR request from the wire. It requires a
// non-empty `model` field and a non-empty `file` part; the decoded audio is
// capped at MaxAudioBodyBytes. Audio contents are never stored beyond the
// returned Request's replay buffer.
func ParseMultipart(request *http.Request) (Request, error) {
	if err := request.ParseMultipartForm(MaxAudioBodyBytes); err != nil {
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
	defer fp.Close()
	fileBytes := make([]byte, file.Size)
	if _, err := io.ReadFull(fp, fileBytes); err != nil {
		return Request{}, invalidRequest("invalid_audio", "file", "The uploaded audio file could not be read.")
	}
	return Request{
		file:        file,
		fileBytes:   fileBytes,
		model:       model,
		language:    firstValue(values["language"]),
		prompt:      firstValue(values["prompt"]),
		granularity: firstValue(values["timestamp_granularity"]),
		responseFmt: firstValue(values["response_format"]),
	}, nil
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

// FileBytes returns the decoded audio bytes for replay. The caller must not
// retain the slice beyond the request lifecycle or log its contents.
func (r Request) FileBytes() []byte { return r.fileBytes }

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
	part, err := writer.CreateFormFile("file", r.file.Filename)
	if err != nil {
		return nil, "", fmt.Errorf("create file part: %w", err)
	}
	if _, err := part.Write(r.fileBytes); err != nil {
		return nil, "", fmt.Errorf("write file bytes: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("close multipart writer: %w", err)
	}
	return []byte(body.String()), writer.FormDataContentType(), nil
}

// AudioContentType reports a plausible content type for the upload so the
// multipart part carries a meaningful Content-Type. Unknown extensions fall
// back to application/octet-stream.
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
