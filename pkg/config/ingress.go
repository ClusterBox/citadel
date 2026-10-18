package config

import (
	"fmt"
	"sort"
)

// Ingress is how an environment's service is reached from the internet.
type Ingress string

const (
	// IngressALB fronts the service with an Application Load Balancer (and
	// CloudFront when cloudfront.enabled). The default.
	IngressALB Ingress = "alb"
	// IngressCloudflareTunnel runs a cloudflared sidecar that dials out to
	// Cloudflare, so the environment needs no load balancer, no CloudFront
	// and no inbound rule. The tunnel and its public hostname are created in
	// Cloudflare (origin: http://localhost:<container.port>); its token is
	// read from SSM at /<name>-<env>/CLOUDFLARE_TUNNEL_TOKEN.
	IngressCloudflareTunnel Ingress = "cloudflare_tunnel"
)

// TunnelTokenSecret is the SSM parameter (under the service's /<name>-<env>/
// prefix) holding the Cloudflare tunnel token. Only the cloudflared sidecar
// receives it, as TUNNEL_TOKEN.
const TunnelTokenSecret = "CLOUDFLARE_TUNNEL_TOKEN"

// ResolvedIngress returns the ingress with the implicit default (alb) applied.
func (e *EnvConfig) ResolvedIngress() Ingress {
	if e.Ingress == "" {
		return IngressALB
	}
	return e.Ingress
}

// validateIngress checks every environment's ingress. The tunnel token is
// handed to the sidecar alone, so the app container may not also declare it.
func (c *DeployConfig) validateIngress() error {
	envs := make([]string, 0, len(c.Environments))
	for env := range c.Environments {
		envs = append(envs, env)
	}
	sort.Strings(envs)
	tunnel := false
	for _, env := range envs {
		e := c.Environments[env]
		switch e.ResolvedIngress() {
		case IngressALB:
		case IngressCloudflareTunnel:
			tunnel = true
		default:
			return fmt.Errorf("environments.%s.ingress: %q must be %q or %q", env, e.Ingress, IngressALB, IngressCloudflareTunnel)
		}
	}
	if !tunnel {
		return nil
	}
	if _, ok := c.Env[TunnelTokenSecret]; ok {
		return fmt.Errorf("%s is set by ingress: %s; remove it from env", TunnelTokenSecret, IngressCloudflareTunnel)
	}
	for _, s := range c.Secrets {
		if s == TunnelTokenSecret {
			return fmt.Errorf("%s is set by ingress: %s; remove it from secrets", TunnelTokenSecret, IngressCloudflareTunnel)
		}
	}
	return nil
}
