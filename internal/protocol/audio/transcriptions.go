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
// It must remain <= router.maxReplayBytes: the inbound cap rejects oversized
// requests before EncodeUpstream rebuilds the body, so a rebuilt body in the
// happy path stays inside the replay limit. Keeping the two thresholds aligned
// means the EncodeUpstream overflow branch (and its 413 mapping via
// router.BodyTooLarge) stays latent unless a future change widens the inbound cap.
const MaxAudioBodyBytes = 25 << 20

// supportedResponseFormats are the response formats the proxy validates and
// relays. Non-JSON formats (srt/vtt/text) are rejected at parse time: the proxy
// validates 2xx bodies as JSON, and forwarding a text format would fail
// validation after the first attempt, burning a key on a protocol error.
var supportedResponseFormats = map[string]struct{}{
	"json":         {},
	"verbose_json": {},
}

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
	// tempDir is the request-scoped scratch directory passed to ParseMultipart.
	// EncodeUpstream reuses it for the rebuilt multipart body so large audio
	// uploads stay clear of the OS tmpfs and the inbound cap pairing.
	tempDir string
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
	if len(files) != 1 {
		return Request{}, invalidRequest("invalid_audio", "file", "Exactly one audio file is required.")
	}
	file := files[0]
	if file.Size == 0 {
		return Request{}, invalidRequest("invalid_audio", "file", "The uploaded audio file is empty.")
	}
	if file.Size > MaxAudioBodyBytes {
		return Request{}, invalidRequest("invalid_audio", "file", "The uploaded audio exceeds the 25 MiB limit.")
	}
	responseFmt := firstValue(values["response_format"])
	if responseFmt != "" {
		if _, ok := supportedResponseFormats[responseFmt]; !ok {
			return Request{}, invalidRequest("unsupported_response_format", "response_format",
				"Only json and verbose_json response formats are supported.")
		}
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
		responseFmt: responseFmt,
		tempDir:     tempDir,
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

// ResponseFormat reports the requested response_format. The HTTP layer uses it
// to pick the upstream response validation budget: verbose_json carries
// per-word timestamps and needs a larger cap than plain json.
func (r Request) ResponseFormat() string { return r.responseFmt }

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
	defer func() { _ = reader.Close() }()
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
// granularity). It returns a replayable body and the content-type header carrying
// the multipart boundary.
//
// The rebuilt body is streamed directly to a temp file via
// router.CaptureStreamedReplay rather than accumulated in a strings.Builder: a
// 25 MiB upload would otherwise sit fully in RAM through every replay attempt.
// The temp file lives in the request-s scoped tempDir passed to ParseMultipart
// so it stays clear of the OS tmpfs.
func (r Request) EncodeUpstream(upstreamModel string) (router.ReplayableBody, string, error) {
	replay, contentType, err := router.CaptureStreamedReplay[string](r.tempDir, func(out io.Writer) (int64, string, error) {
		// CaptureStreamedReplay rejects bodies > maxReplayBytes, so every byte
		// written through the multipart writer must be counted toward the limit.
		// Using io.Copy alone would miss field/header bytes and the terminating
		// boundary, so wrap the destination in a counter.
		counter := &countingWriter{writer: out}
		writer := multipart.NewWriter(counter)
		contentType := writer.FormDataContentType()
		writeErr := func(format string, cause error) (int64, string, error) {
			return counter.bytes, contentType, fmt.Errorf(format, cause)
		}
		if err := writer.WriteField("model", upstreamModel); err != nil {
			return writeErr("write model field: %w", err)
		}
		if r.language != "" {
			if err := writer.WriteField("language", r.language); err != nil {
				return writeErr("write language field: %w", err)
			}
		}
		if r.prompt != "" {
			if err := writer.WriteField("prompt", r.prompt); err != nil {
				return writeErr("write prompt field: %w", err)
			}
		}
		if r.responseFmt != "" {
			if err := writer.WriteField("response_format", r.responseFmt); err != nil {
				return writeErr("write response_format field: %w", err)
			}
		}
		if r.granularity != "" {
			if err := writer.WriteField("timestamp_granularity", r.granularity); err != nil {
				return writeErr("write timestamp field: %w", err)
			}
		}
		part, err := writer.CreatePart(filePartHeader(r.file.Filename, r.AudioContentType()))
		if err != nil {
			return writeErr("create file part: %w", err)
		}
		reader, err := r.fileBody.Open()
		if err != nil {
			return writeErr("open file body: %w", err)
		}
		if _, err := io.Copy(part, reader); err != nil {
			_ = reader.Close()
			return writeErr("write file bytes: %w", err)
		}
		if err := reader.Close(); err != nil {
			return writeErr("close file body: %w", err)
		}
		if err := writer.Close(); err != nil {
			return writeErr("close multipart writer: %w", err)
		}
		return counter.bytes, contentType, nil
	})
	if err != nil {
		return nil, "", fmt.Errorf("capture upstream multipart body: %w", err)
	}
	if r.fileBody != nil {
		if err := r.fileBody.Close(); err != nil {
			_ = replay.Close()
			return nil, "", fmt.Errorf("release source audio replay body: %w", err)
		}
	}
	return replay, contentType, nil
}

// countingWriter transparently counts every byte written through it so the
// caller can report an accurate N to CaptureStreamedReplay without needing to
// know how the multipart writer packages fields and boundaries.
type countingWriter struct {
	writer io.Writer
	bytes  int64
}

func (c *countingWriter) Write(data []byte) (int, error) {
	n, err := c.writer.Write(data)
	if n > 0 {
		c.bytes += int64(n)
	}
	return n, err
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
