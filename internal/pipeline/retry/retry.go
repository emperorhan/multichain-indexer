package retry

import (
	"context"
	"errors"
	"net"
	"strings"

	baserpc "github.com/emperorhan/multichain-indexer/internal/chain/base/rpc"
	solanarpc "github.com/emperorhan/multichain-indexer/internal/chain/solana/rpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Class string

const (
	ClassTerminal  Class = "terminal"
	ClassTransient Class = "transient"
)

type Decision struct {
	Class  Class
	Reason string
}

func (d Decision) IsTransient() bool {
	return d.Class == ClassTransient
}

type classifiedError struct {
	err    error
	class  Class
	reason string
}

func (e *classifiedError) Error() string {
	return e.err.Error()
}

func (e *classifiedError) Unwrap() error {
	return e.err
}

func Transient(err error) error {
	if err == nil {
		return nil
	}
	return &classifiedError{
		err:    err,
		class:  ClassTransient,
		reason: "explicit_transient",
	}
}

func Terminal(err error) error {
	if err == nil {
		return nil
	}
	return &classifiedError{
		err:    err,
		class:  ClassTerminal,
		reason: "explicit_terminal",
	}
}

func Classify(err error) Decision {
	if err == nil {
		return Decision{Class: ClassTerminal, Reason: "nil_error"}
	}

	var marked *classifiedError
	if errors.As(err, &marked) {
		return Decision{Class: marked.class, Reason: marked.reason}
	}

	if errors.Is(err, context.Canceled) {
		return Decision{Class: ClassTerminal, Reason: "context_canceled"}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return Decision{Class: ClassTransient, Reason: "context_deadline_exceeded"}
	}

	if grpcStatus, ok := status.FromError(err); ok {
		switch grpcStatus.Code() {
		case codes.Canceled:
			return Decision{Class: ClassTerminal, Reason: "grpc_canceled"}
		case codes.Unavailable, codes.DeadlineExceeded, codes.ResourceExhausted, codes.Aborted, codes.Internal:
			return Decision{Class: ClassTransient, Reason: "grpc_" + strings.ToLower(grpcStatus.Code().String())}
		default:
			return Decision{Class: ClassTerminal, Reason: "grpc_" + strings.ToLower(grpcStatus.Code().String())}
		}
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return Decision{Class: ClassTransient, Reason: "net_timeout"}
		}
	}

	var solErr *solanarpc.RPCError
	if errors.As(err, &solErr) {
		return classifyJSONRPCCode(solErr.Code)
	}
	var baseErr *baserpc.RPCError
	if errors.As(err, &baseErr) {
		return classifyJSONRPCCode(baseErr.Code)
	}

	lower := strings.ToLower(err.Error())
	if containsAny(lower, terminalMessageTokens) {
		return Decision{Class: ClassTerminal, Reason: "message_terminal"}
	}
	if containsAny(lower, transientMessageTokens) {
		return Decision{Class: ClassTransient, Reason: "message_transient"}
	}

	return Decision{Class: ClassTerminal, Reason: "unknown_terminal_default"}
}

func classifyJSONRPCCode(code int) Decision {
	if code == -32603 || code == -32005 {
		return Decision{Class: ClassTransient, Reason: "jsonrpc_server_transient"}
	}
	if code <= -32000 && code >= -32099 {
		return Decision{Class: ClassTransient, Reason: "jsonrpc_server_range"}
	}
	return Decision{Class: ClassTerminal, Reason: "jsonrpc_terminal"}
}

func containsAny(msg string, tokens []string) bool {
	for _, token := range tokens {
		if strings.Contains(msg, token) {
			return true
		}
	}
	return false
}

var transientMessageTokens = []string{
	"timeout",
	"timed out",
	"temporar",
	"temporary",
	"unavailable",
	"connection reset",
	"connection refused",
	"broken pipe",
	"econnreset",
	"econnrefused",
	"too many requests",
	"rate limit",
	"payload too large",
	"http status 429",
	"http status 502",
	"http status 503",
	"http status 504",
	"server closed idle connection",
}

var terminalMessageTokens = []string{
	"decode blocked",
	"length mismatch",
	"no decodable transactions",
	"invalid argument",
	"invalid params",
	"method not found",
	"parse error",
	"execution reverted",
	"insufficient funds",
	"not found",
	"constraint violation",
}
