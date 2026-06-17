package forge

import (
	"net/http"

	giteasdk "gitea.dev/sdk"
)

// NewTestClient builds a Client with a custom http.Client for use in tests.
// The custom http.Client is passed via the SDK's SetHTTPClient option so no
// TCP listener is bound — sandbox-safe (#73).
//
// The handler passed to the underlying http.Client must return a valid Gitea
// JSON error body (e.g. `{"message":"not found"}`) for any non-2xx response —
// the SDK's statusCodeToErr will panic on a nil body.
func NewTestClient(baseURL, owner, repo, token string, hc *http.Client) *Client {
	sdk, _ := giteasdk.NewClient(baseURL,
		giteasdk.SetToken(token),
		giteasdk.SetHTTPClient(hc),
		giteasdk.SetGiteaVersion(""),
	)
	return &Client{BaseURL: baseURL + "/", Owner: owner, Repo: repo, Token: token, sdk: sdk}
}
