/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package awsprice prices EC2 nodes with the AWS Pricing API (on-demand) and
// EC2 spot price history (spot), using whatever credentials the local
// environment already provides. All calls are read-only.
package awsprice

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/pricing"
	pricingtypes "github.com/aws/aws-sdk-go-v2/service/pricing/types"

	viewerpricing "github.com/lavluda/k8s-pods-viewer/pkg/pricing"
)

const (
	// pricingRegion is where the Pricing API is reachable in the commercial
	// partition; prices themselves are selected with the regionCode filter.
	pricingRegion = "us-east-1"

	// onDemandTTL is long because published on-demand prices change rarely,
	// while spotTTL keeps spot prices close to the live market.
	onDemandTTL = 12 * time.Hour
	spotTTL     = 10 * time.Minute

	// fetchInterval is how often the background loop drains newly seen nodes.
	fetchInterval = 15 * time.Second

	spotLookback = time.Hour
)

type pricingAPI interface {
	GetProducts(ctx context.Context, params *pricing.GetProductsInput, optFns ...func(*pricing.Options)) (*pricing.GetProductsOutput, error)
}

type spotAPI interface {
	DescribeSpotPriceHistory(ctx context.Context, params *ec2.DescribeSpotPriceHistoryInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSpotPriceHistoryOutput, error)
}

type entry struct {
	price     float64
	refreshed time.Time
}

// Provider is an AWS-backed viewerpricing.Provider. Lookups are served from an
// in-memory cache; unknown nodes are queued and priced by a background loop, so
// the render path never blocks on AWS.
type Provider struct {
	mu       sync.RWMutex
	onDemand map[string]entry
	spot     map[string]entry
	// pending holds specs seen since the last fetch; tracked holds every spec
	// ever seen so stale prices can be refreshed on their own TTL.
	pending     map[viewerpricing.NodeSpec]struct{}
	tracked     map[viewerpricing.NodeSpec]struct{}
	unavailable string
	priced      bool
	// wake is a buffered signal so enqueueing a new pending spec can interrupt
	// the fetch-interval sleep without queuing duplicate refreshes.
	wake chan struct{}

	profile string

	// clientMu guards lazy client construction.
	clientMu   sync.Mutex
	pricing    pricingAPI
	spotByReg  map[string]spotAPI
	newPricing func(ctx context.Context) (pricingAPI, error)
	newSpot    func(ctx context.Context, region string) (spotAPI, error)
}

// New returns a provider and starts its background refresh loop. It never fails:
// credential or permission problems surface through Unavailable so the UI can
// show a reason instead of dying.
func New(ctx context.Context, profile string) *Provider {
	p := newProvider(profile)
	p.newPricing = p.defaultPricingClient
	p.newSpot = p.defaultSpotClient
	go p.run(ctx)
	return p
}

func newProvider(profile string) *Provider {
	return &Provider{
		onDemand:  map[string]entry{},
		spot:      map[string]entry{},
		pending:   map[viewerpricing.NodeSpec]struct{}{},
		tracked:   map[viewerpricing.NodeSpec]struct{}{},
		wake:      make(chan struct{}, 1),
		spotByReg: map[string]spotAPI{},
		profile:   profile,
	}
}

// HourlyPrice implements viewerpricing.Provider.
func (p *Provider) HourlyPrice(spec viewerpricing.NodeSpec) (float64, bool) {
	if !spec.Priceable() {
		return 0, false
	}

	p.mu.RLock()
	price, ok := p.lookupLocked(spec)
	p.mu.RUnlock()
	if ok {
		return price, true
	}

	p.mu.Lock()
	cached := cacheSpec(spec)
	_, alreadyPending := p.pending[cached]
	_, alreadyTracked := p.tracked[cached]
	p.pending[cached] = struct{}{}
	p.mu.Unlock()
	// Wake only for first sighting of a shape so cold-start prices arrive
	// promptly, while retries for tracked/failed specs stay on the ticker.
	if !alreadyPending && !alreadyTracked {
		p.notify()
	}
	return 0, false
}

// notify wakes the background loop if it is sleeping between refreshes.
func (p *Provider) notify() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// Unavailable implements viewerpricing.Provider.
func (p *Provider) Unavailable() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.priced {
		return ""
	}
	return p.unavailable
}

func (p *Provider) lookupLocked(spec viewerpricing.NodeSpec) (float64, bool) {
	if spec.Spot() {
		if e, ok := p.spot[spotKey(spec)]; ok {
			return e.price, true
		}
		// Fall through: an on-demand price is a better answer than none, and it
		// is the ceiling of what the spot node can cost.
	}
	if e, ok := p.onDemand[onDemandKey(spec)]; ok {
		return e.price, true
	}
	return 0, false
}

// run drives the background fetch loop until ctx is done.
func (p *Provider) run(ctx context.Context) {
	ticker := time.NewTicker(fetchInterval)
	defer ticker.Stop()
	for {
		p.refresh(ctx)
		select {
		case <-ctx.Done():
			return
		case <-p.wake:
			// A newly pending spec arrived; refresh without waiting out the interval.
		case <-ticker.C:
		}
	}
}

// refresh prices every queued node plus any cached price that has gone stale.
func (p *Provider) refresh(ctx context.Context) {
	specs := p.dueSpecs()
	if len(specs) == 0 {
		return
	}

	onDemandWanted := map[string]viewerpricing.NodeSpec{}
	spotWanted := map[string][]viewerpricing.NodeSpec{}
	for _, spec := range specs {
		onDemandWanted[onDemandKey(spec)] = spec
		if spec.Spot() {
			region := spec.EffectiveRegion()
			spotWanted[region] = append(spotWanted[region], spec)
		}
	}

	for key, spec := range onDemandWanted {
		price, err := p.fetchOnDemand(ctx, spec)
		if err != nil {
			p.recordFailure(fmt.Errorf("on-demand price for %s: %w", spec.InstanceType, err))
			continue
		}
		if price <= 0 {
			continue
		}
		p.store(&p.onDemand, key, price)
	}

	for region, regionSpecs := range spotWanted {
		prices, err := p.fetchSpot(ctx, region, regionSpecs)
		if err != nil {
			p.recordFailure(fmt.Errorf("spot prices in %s: %w", region, err))
			continue
		}
		for key, price := range prices {
			p.store(&p.spot, key, price)
		}
	}
}

// dueSpecs returns the queued specs plus the cached ones whose TTL expired, and
// clears the queue.
func (p *Provider) dueSpecs() []viewerpricing.NodeSpec {
	p.mu.Lock()
	defer p.mu.Unlock()

	specs := make([]viewerpricing.NodeSpec, 0, len(p.pending))
	seen := map[viewerpricing.NodeSpec]struct{}{}
	for spec := range p.pending {
		specs = append(specs, spec)
		seen[spec] = struct{}{}
		p.tracked[spec] = struct{}{}
		delete(p.pending, spec)
	}

	now := time.Now()
	for spec := range p.tracked {
		if _, ok := seen[spec]; ok {
			continue
		}
		ttl := onDemandTTL
		if spec.Spot() {
			ttl = spotTTL
		}
		refreshed := p.onDemand[onDemandKey(spec)].refreshed
		if spec.Spot() {
			refreshed = p.spot[spotKey(spec)].refreshed
		}
		if now.Sub(refreshed) >= ttl {
			specs = append(specs, spec)
		}
	}
	return specs
}

func (p *Provider) store(cache *map[string]entry, key string, price float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	(*cache)[key] = entry{price: price, refreshed: time.Now()}
	p.priced = true
	p.unavailable = ""
}

func (p *Provider) recordFailure(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.unavailable = shortReason(err)
}

func (p *Provider) fetchOnDemand(ctx context.Context, spec viewerpricing.NodeSpec) (float64, error) {
	client, err := p.pricingClient(ctx)
	if err != nil {
		return 0, err
	}

	filters := []pricingtypes.Filter{
		termFilter("instanceType", spec.InstanceType),
		termFilter("regionCode", spec.EffectiveRegion()),
		termFilter("operatingSystem", pricingOS(spec.OS)),
		termFilter("tenancy", "Shared"),
		termFilter("preInstalledSw", "NA"),
		termFilter("capacitystatus", "Used"),
		termFilter("licenseModel", "No License required"),
	}
	out, err := client.GetProducts(ctx, &pricing.GetProductsInput{
		ServiceCode: aws.String("AmazonEC2"),
		Filters:     filters,
		MaxResults:  aws.Int32(10),
	})
	if err != nil {
		return 0, err
	}
	return lowestOnDemandPrice(out.PriceList)
}

func (p *Provider) fetchSpot(ctx context.Context, region string, specs []viewerpricing.NodeSpec) (map[string]float64, error) {
	client, err := p.spotClient(ctx, region)
	if err != nil {
		return nil, err
	}

	instanceTypes := make([]ec2types.InstanceType, 0, len(specs))
	descriptions := map[string]struct{}{}
	seenType := map[string]struct{}{}
	for _, spec := range specs {
		if _, ok := seenType[spec.InstanceType]; !ok {
			seenType[spec.InstanceType] = struct{}{}
			instanceTypes = append(instanceTypes, ec2types.InstanceType(spec.InstanceType))
		}
		descriptions[spotProductDescription(spec.OS)] = struct{}{}
	}
	products := make([]string, 0, len(descriptions))
	for description := range descriptions {
		products = append(products, description)
	}

	out, err := client.DescribeSpotPriceHistory(ctx, &ec2.DescribeSpotPriceHistoryInput{
		InstanceTypes:       instanceTypes,
		ProductDescriptions: products,
		StartTime:           aws.Time(time.Now().Add(-spotLookback)),
	})
	if err != nil {
		return nil, err
	}

	type sample struct {
		price float64
		when  time.Time
	}
	latest := map[string]sample{}
	for _, item := range out.SpotPriceHistory {
		if item.SpotPrice == nil || item.AvailabilityZone == nil {
			continue
		}
		price, err := strconv.ParseFloat(*item.SpotPrice, 64)
		if err != nil || price <= 0 {
			continue
		}
		when := time.Time{}
		if item.Timestamp != nil {
			when = *item.Timestamp
		}
		key := zoneTypeKey(*item.AvailabilityZone, string(item.InstanceType))
		if existing, ok := latest[key]; ok && !when.After(existing.when) {
			continue
		}
		latest[key] = sample{price: price, when: when}
	}

	prices := make(map[string]float64, len(latest))
	for key, s := range latest {
		prices[key] = s.price
	}
	return prices, nil
}

func (p *Provider) pricingClient(ctx context.Context) (pricingAPI, error) {
	p.clientMu.Lock()
	defer p.clientMu.Unlock()
	if p.pricing != nil {
		return p.pricing, nil
	}
	client, err := p.newPricing(ctx)
	if err != nil {
		return nil, err
	}
	p.pricing = client
	return client, nil
}

func (p *Provider) spotClient(ctx context.Context, region string) (spotAPI, error) {
	p.clientMu.Lock()
	defer p.clientMu.Unlock()
	if client, ok := p.spotByReg[region]; ok {
		return client, nil
	}
	client, err := p.newSpot(ctx, region)
	if err != nil {
		return nil, err
	}
	p.spotByReg[region] = client
	return client, nil
}

func (p *Provider) defaultPricingClient(ctx context.Context) (pricingAPI, error) {
	cfg, err := p.awsConfig(ctx, pricingRegion)
	if err != nil {
		return nil, err
	}
	return pricing.NewFromConfig(cfg), nil
}

func (p *Provider) defaultSpotClient(ctx context.Context, region string) (spotAPI, error) {
	cfg, err := p.awsConfig(ctx, region)
	if err != nil {
		return nil, err
	}
	return ec2.NewFromConfig(cfg), nil
}

func (p *Provider) awsConfig(ctx context.Context, region string) (aws.Config, error) {
	options := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(region)}
	if strings.TrimSpace(p.profile) != "" {
		options = append(options, awsconfig.WithSharedConfigProfile(p.profile))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return aws.Config{}, err
	}
	if _, err := cfg.Credentials.Retrieve(ctx); err != nil {
		return aws.Config{}, fmt.Errorf("no AWS credentials: %w", err)
	}
	return cfg, nil
}

func termFilter(field, value string) pricingtypes.Filter {
	return pricingtypes.Filter{
		Type:  pricingtypes.FilterTypeTermMatch,
		Field: aws.String(field),
		Value: aws.String(value),
	}
}

// priceDocument is the slice of an AWS price-list product we care about.
type priceDocument struct {
	Terms struct {
		OnDemand map[string]struct {
			PriceDimensions map[string]struct {
				PricePerUnit struct {
					USD string `json:"USD"`
				} `json:"pricePerUnit"`
			} `json:"priceDimensions"`
		} `json:"OnDemand"`
	} `json:"terms"`
}

// lowestOnDemandPrice picks the cheapest non-zero USD on-demand rate in the
// returned products. Zero-price dimensions exist for some SKUs and must not win.
func lowestOnDemandPrice(priceList []string) (float64, error) {
	best := 0.0
	for _, raw := range priceList {
		var doc priceDocument
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			return 0, fmt.Errorf("decode price list: %w", err)
		}
		for _, term := range doc.Terms.OnDemand {
			for _, dimension := range term.PriceDimensions {
				price, err := strconv.ParseFloat(dimension.PricePerUnit.USD, 64)
				if err != nil || price <= 0 {
					continue
				}
				if best == 0 || price < best {
					best = price
				}
			}
		}
	}
	return best, nil
}

func pricingOS(os string) string {
	if strings.EqualFold(os, "windows") {
		return "Windows"
	}
	return "Linux"
}

func spotProductDescription(os string) string {
	if strings.EqualFold(os, "windows") {
		return "Windows"
	}
	return "Linux/UNIX"
}

// cacheSpec drops the fields that do not affect the price lookup so equivalent
// nodes queue once.
func cacheSpec(spec viewerpricing.NodeSpec) viewerpricing.NodeSpec {
	return viewerpricing.NodeSpec{
		InstanceType: spec.InstanceType,
		Region:       spec.EffectiveRegion(),
		Zone:         spec.Zone,
		CapacityType: spec.CapacityType,
		OS:           strings.ToLower(spec.OS),
	}
}

func onDemandKey(spec viewerpricing.NodeSpec) string {
	return strings.Join([]string{spec.EffectiveRegion(), pricingOS(spec.OS), spec.InstanceType}, "|")
}

func spotKey(spec viewerpricing.NodeSpec) string {
	return zoneTypeKey(spec.Zone, spec.InstanceType)
}

func zoneTypeKey(zone, instanceType string) string {
	return zone + "|" + instanceType
}

// shortReason keeps the tile readable: AWS errors are long, and only the head of
// the message tells the user what to fix.
func shortReason(err error) string {
	message := err.Error()
	if idx := strings.Index(message, "\n"); idx > 0 {
		message = message[:idx]
	}
	switch {
	case strings.Contains(message, "no AWS credentials"):
		return "no AWS credentials"
	case strings.Contains(message, "AccessDenied") || strings.Contains(message, "not authorized"):
		return "pricing access denied"
	case strings.Contains(message, "no such host") || strings.Contains(message, "dial tcp"):
		return "pricing API unreachable"
	}
	const limit = 40
	if len(message) > limit {
		message = message[:limit-1] + "…"
	}
	return message
}
