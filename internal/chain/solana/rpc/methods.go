package rpc

import (
	"context"
	"encoding/json"
	"fmt"
)

// GetSlot returns the current slot.
func (c *Client) GetSlot(ctx context.Context, commitment string) (int64, error) {
	params := []interface{}{
		map[string]string{"commitment": commitment},
	}
	result, err := c.call(ctx, "getSlot", params)
	if err != nil {
		return 0, fmt.Errorf("getSlot: %w", err)
	}

	var slot int64
	if err := json.Unmarshal(result, &slot); err != nil {
		return 0, fmt.Errorf("unmarshal slot: %w", err)
	}
	return slot, nil
}

// GetSignaturesForAddress returns transaction signatures for an address.
// Results are returned newest-first by default.
func (c *Client) GetSignaturesForAddress(ctx context.Context, address string, opts *GetSignaturesOpts) ([]SignatureInfo, error) {
	config := map[string]interface{}{
		"commitment": "confirmed",
	}
	if opts != nil {
		if opts.Limit > 0 {
			config["limit"] = opts.Limit
		}
		if opts.Before != "" {
			config["before"] = opts.Before
		}
		if opts.Until != "" {
			config["until"] = opts.Until
		}
	}

	params := []interface{}{address, config}
	result, err := c.call(ctx, "getSignaturesForAddress", params)
	if err != nil {
		return nil, fmt.Errorf("getSignaturesForAddress: %w", err)
	}

	var sigs []SignatureInfo
	if err := json.Unmarshal(result, &sigs); err != nil {
		return nil, fmt.Errorf("unmarshal signatures: %w", err)
	}
	return sigs, nil
}

type GetSignaturesOpts struct {
	Limit  int
	Before string // signature to start searching backwards from
	Until  string // signature to search until (exclusive)
}

// GetTransaction returns a parsed transaction by signature.
func (c *Client) GetTransaction(ctx context.Context, signature string) (json.RawMessage, error) {
	params := buildGetTransactionParams(signature)
	result, err := c.call(ctx, "getTransaction", params)
	if err != nil {
		return nil, fmt.Errorf("getTransaction(%s): %w", signature, err)
	}
	return result, nil
}

func (c *Client) GetTransactions(ctx context.Context, signatures []string) ([]json.RawMessage, error) {
	if len(signatures) == 0 {
		return []json.RawMessage{}, nil
	}

	requests := make([]Request, len(signatures))
	for i, signature := range signatures {
		requests[i] = c.newRequest("getTransaction", buildGetTransactionParams(signature))
	}

	responses, err := c.callBatch(ctx, requests)
	if err != nil {
		return nil, fmt.Errorf("getTransaction batch: %w", err)
	}

	results := make([]json.RawMessage, len(signatures))
	for i, response := range responses {
		if response.Error != nil {
			return nil, fmt.Errorf("getTransaction(%s): %w", signatures[i], response.Error)
		}
		results[i] = response.Result
	}
	return results, nil
}

func buildGetTransactionParams(signature string) []interface{} {
	return []interface{}{
		signature,
		map[string]interface{}{
			"encoding":                       "jsonParsed",
			"commitment":                     "confirmed",
			"maxSupportedTransactionVersion": 0,
		},
	}
}
