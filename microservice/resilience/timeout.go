package resilience

import (
	"context"
	"log"
	"time"

	"google.golang.org/grpc"
)

// TimeoutUnaryClientInterceptor implements the Timeout
// resilience pattern: it bounds how long a single RPC may run,
// independent of whatever deadline (if any) the caller's
// context already carries. Without it, a slow dependency can
// stall a request for as long as that dependency takes (or
// forever, if it never responds); with it, the call fails
// fast and predictably once its own budget runs out.
func TimeoutUnaryClientInterceptor(timeout time.Duration) grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		req, reply interface{},
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {

		callCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		start := time.Now()
		err := invoker(callCtx, method, req, reply, cc, opts...)
		if err != nil && callCtx.Err() == context.DeadlineExceeded {
			log.Printf("[timeout] %s exceeded its %s budget (ran %s), aborting", method, timeout, time.Since(start).Round(time.Millisecond))
		}
		return err
	}
}
