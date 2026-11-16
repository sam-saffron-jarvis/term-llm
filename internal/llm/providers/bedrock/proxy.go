package bedrock

import (
	"github.com/term-llm/term-llm/internal/awsauth"
	"github.com/term-llm/term-llm/internal/proxyclient"
	"os"
)

func resolveEndpointAuth(baseURL, region, profile, accessKey, secretKey, sessionToken string) (string, awsauth.CredentialsProvider, string, error) {
	if b, ok := proxyclient.Lookup(baseURL); ok && b.Kind == "aws-sigv4" {
		if region == "" {
			region = "us-east-1"
		}
		return region, nil, b.Token, nil
	}
	bearer := os.Getenv("AWS_BEARER_TOKEN_BEDROCK")
	resolved, provider, err := resolveBedrockAuth(region, profile, accessKey, secretKey, sessionToken, bearer)
	return resolved, provider, bearer, err
}
