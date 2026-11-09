package gql

import (
	"context"
	"net/http"
	"time"

	"github.com/99designs/gqlgen/graphql"

	"github.com/looplj/axonhub/internal/log"
)

type requestTimeoutKey struct{}

type requestTimeout struct {
	processing time.Duration
	writing    time.Duration
	response   *http.ResponseController
	cancel     context.CancelFunc
}

// WithRequestTimeout 分别限制完整请求的接收和已解析操作的执行。
// gqlgen 自身负责读取正文；这里不复制正文，也不改变文件上传的临时存储。
func WithRequestTimeout(next http.Handler, processing, receive, writing time.Duration) http.Handler {
	if receive <= 0 {
		receive = processing
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		controller := http.NewResponseController(w)
		state := &requestTimeout{processing: processing, writing: writing, response: controller}
		if writing > 0 {
			if err := controller.SetWriteDeadline(time.Now().Add(max(receive, 0) + writing)); err != nil {
				log.Error(r.Context(), "could not set admin response write deadline", log.Cause(err))
				http.Error(w, "could not set response write deadline", http.StatusInternalServerError)
				return
			}
		}
		if receive > 0 {
			if err := controller.SetReadDeadline(time.Now().Add(receive)); err != nil {
				log.Error(r.Context(), "could not set admin request read deadline", log.Cause(err))
				http.Error(w, "could not set request read deadline", http.StatusInternalServerError)
				return
			}
		}
		defer func() {
			if state.cancel != nil {
				state.cancel()
			}
			if receive > 0 {
				_ = controller.SetReadDeadline(time.Time{})
			}
		}()
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestTimeoutKey{}, state)))
	})
}

func withOperationTimeout(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
	if diagnostics, ok := ctx.Value(requestDiagnosticsKey{}).(*requestDiagnostics); ok {
		diagnostics.phase = "execution"
	}
	state, _ := ctx.Value(requestTimeoutKey{}).(*requestTimeout)
	if state == nil {
		return next(ctx)
	}
	if err := state.response.SetReadDeadline(time.Time{}); err != nil {
		log.Error(ctx, "could not clear admin request read deadline", log.Cause(err))
		return graphql.OneShot(graphql.ErrorResponse(ctx, "could not finish receiving request"))
	}
	if state.writing > 0 {
		if err := state.response.SetWriteDeadline(time.Now().Add(state.writing)); err != nil {
			log.Error(ctx, "could not renew admin response write deadline", log.Cause(err))
			return graphql.OneShot(graphql.ErrorResponse(ctx, "could not start response write deadline"))
		}
	}
	ctx, state.cancel = context.WithTimeout(ctx, state.processing)
	return next(ctx)
}
