package server

import (
	"context"
	"mime"
	"net/http"
	"os"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/jclab-joseph/agent-web/internal/model"
)

// maxUploadBytes bounds one upload request.
const maxUploadBytes = 200 << 20

type uploadForm struct {
	Files []huma.FormFile `form:"files" required:"true" doc:"Files to upload"`
}

func (s *Server) registerUploads() {
	huma.Register(s.api, huma.Operation{OperationID: "uploadFiles", Method: http.MethodPost, Path: "/api/sessions/{id}/uploads",
		Tags: []string{"sessions"}, Summary: "Upload files for a prompt",
		Description: "Saves the files in the session's upload directory ($AGENT_UPLOAD_DIR) as <name>.<ext>, " +
			"or <name>-<n>.<ext> when the name is taken. Attach them with sendPrompt's files.",
		MaxBodyBytes: maxUploadBytes, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *struct {
			SessionPath
			RawBody huma.MultipartFormFiles[uploadForm]
		}) (*struct{ Body []model.Upload }, error) {
			if in.RawBody.Form != nil {
				defer in.RawBody.Form.RemoveAll()
			}
			sess, err := s.m.Get(in.ID)
			if err != nil {
				return nil, apiError(err)
			}
			out := &struct{ Body []model.Upload }{Body: []model.Upload{}}
			for _, f := range in.RawBody.Data().Files {
				u, err := sess.SaveUpload(f.Filename, f)
				f.Close()
				if err != nil {
					return nil, apiError(err)
				}
				out.Body = append(out.Body, u)
			}
			return out, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "getUpload", Method: http.MethodGet, Path: "/api/sessions/{id}/uploads/{name}",
		Tags: []string{"sessions"}, Summary: "An uploaded file",
		Responses: map[string]*huma.Response{"200": {Description: "The file", Content: map[string]*huma.MediaType{
			"application/octet-stream": {Schema: &huma.Schema{Type: huma.TypeString, Format: "binary"}}}}}},
		func(ctx context.Context, in *struct {
			SessionPath
			Name     string `path:"name"`
			Download bool   `query:"download"`
		}) (*huma.StreamResponse, error) {
			sess, err := s.m.Get(in.ID)
			if err != nil {
				return nil, apiError(err)
			}
			u, err := sess.Upload(in.Name)
			if err != nil {
				return nil, apiError(err)
			}
			f, err := os.Open(u.Path)
			if err != nil {
				return nil, huma.Error410Gone("the file is no longer available")
			}
			fi, err := f.Stat()
			if err != nil {
				f.Close()
				return nil, huma.Error410Gone("the file is no longer available")
			}
			disposition := "inline"
			if in.Download {
				disposition = "attachment"
			}
			return &huma.StreamResponse{Body: func(hctx huma.Context) {
				defer f.Close()
				r, w := humago.Unwrap(hctx)
				w.Header().Set("Content-Type", u.MediaType)
				w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": u.Name}))
				w.Header().Set("X-Content-Type-Options", "nosniff")
				w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self' data:; media-src 'self'; style-src 'unsafe-inline'")
				http.ServeContent(w, r, u.Name, fi.ModTime(), f)
			}}, nil
		})
}
