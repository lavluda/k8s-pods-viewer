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

package awsprice

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/pricing"

	viewerpricing "github.com/lavluda/k8s-pods-viewer/pkg/pricing"
)

func priceListDoc(usd string) string {
	return fmt.Sprintf(`{"terms":{"OnDemand":{"sku.1":{"priceDimensions":{"sku.1.dim":{"pricePerUnit":{"USD":"%s"}}}}}}}`, usd)
}

type fakePricing struct {
	calls    int
	requests []*pricing.GetProductsInput
	docs     []string
	err      error
}

func (f *fakePricing) GetProducts(_ context.Context, params *pricing.GetProductsInput, _ ...func(*pricing.Options)) (*pricing.GetProductsOutput, error) {
	f.calls++
	f.requests = append(f.requests, params)
	if f.err != nil {
		return nil, f.err
	}
	return &pricing.GetProductsOutput{PriceList: f.docs}, nil
}

type fakeSpot struct {
	calls   int
	regions []string
	history []ec2types.SpotPrice
	err     error
}

func (f *fakeSpot) DescribeSpotPriceHistory(_ context.Context, _ *ec2.DescribeSpotPriceHistoryInput, _ ...func(*ec2.Options)) (*ec2.DescribeSpotPriceHistoryOutput, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return &ec2.DescribeSpotPriceHistoryOutput{SpotPriceHistory: f.history}, nil
}

func testProvider(t *testing.T, pricingClient pricingAPI, spotClient spotAPI) *Provider {
	t.Helper()
	p := newProvider("")
	p.newPricing = func(context.Context) (pricingAPI, error) {
		if pricingClient == nil {
			return nil, errors.New("no AWS credentials: not configured")
		}
		return pricingClient, nil
	}
	p.newSpot = func(_ context.Context, region string) (spotAPI, error) {
		if spotClient == nil {
			return nil, errors.New("no AWS credentials: not configured")
		}
		if fake, ok := spotClient.(*fakeSpot); ok {
			fake.regions = append(fake.regions, region)
		}
		return spotClient, nil
	}
	return p
}

func TestProviderEnqueueWakesRefreshLoop(t *testing.T) {
	pricingClient := &fakePricing{docs: []string{priceListDoc("0.1070")}}
	p := testProvider(t, pricingClient, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.run(ctx)

	spec := viewerpricing.NodeSpec{InstanceType: "m5.large", Zone: "eu-west-1a", CapacityType: "on-demand", OS: "linux"}

	// Without a wake, run() would sleep fetchInterval (~15s) after the empty
	// initial refresh. Prices must arrive well before that.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if price, ok := p.HourlyPrice(spec); ok {
			if price != 0.107 {
				t.Fatalf("HourlyPrice() = %v, want 0.107", price)
			}
			if pricingClient.calls < 1 {
				t.Fatalf("GetProducts calls = %d, want at least 1", pricingClient.calls)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("price not available within 2s; enqueue did not wake the refresh loop")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestProviderPricesOnDemandNodeAfterRefresh(t *testing.T) {
	pricingClient := &fakePricing{docs: []string{priceListDoc("0.1070")}}
	p := testProvider(t, pricingClient, nil)
	spec := viewerpricing.NodeSpec{InstanceType: "m5.large", Zone: "eu-west-1a", CapacityType: "on-demand", OS: "linux"}

	if _, ok := p.HourlyPrice(spec); ok {
		t.Fatal("HourlyPrice() = ok before any refresh")
	}

	p.refresh(context.Background())
	price, ok := p.HourlyPrice(spec)
	if !ok || price != 0.107 {
		t.Fatalf("HourlyPrice() = %v, %v; want 0.107, true", price, ok)
	}
	if got := p.Unavailable(); got != "" {
		t.Fatalf("Unavailable() = %q, want empty", got)
	}
	if pricingClient.calls != 1 {
		t.Fatalf("GetProducts calls = %d, want 1", pricingClient.calls)
	}

	// A second node of the same shape must be served from cache.
	if _, ok := p.HourlyPrice(viewerpricing.NodeSpec{InstanceType: "m5.large", Zone: "eu-west-1b", OS: "linux"}); !ok {
		t.Fatal("second node of the same type was not served from cache")
	}
	p.refresh(context.Background())
	if pricingClient.calls != 1 {
		t.Fatalf("GetProducts calls = %d after cached lookup, want 1", pricingClient.calls)
	}

	filters := map[string]string{}
	for _, filter := range pricingClient.requests[0].Filters {
		filters[aws.ToString(filter.Field)] = aws.ToString(filter.Value)
	}
	for field, want := range map[string]string{
		"instanceType":    "m5.large",
		"regionCode":      "eu-west-1",
		"operatingSystem": "Linux",
		"tenancy":         "Shared",
		"capacitystatus":  "Used",
	} {
		if filters[field] != want {
			t.Errorf("filter %s = %q, want %q", field, filters[field], want)
		}
	}
}

func TestProviderPricesSpotNodePerZone(t *testing.T) {
	now := time.Now()
	pricingClient := &fakePricing{docs: []string{priceListDoc("0.1070")}}
	spotClient := &fakeSpot{history: []ec2types.SpotPrice{
		{
			AvailabilityZone: aws.String("eu-west-1a"),
			InstanceType:     ec2types.InstanceType("m5.large"),
			SpotPrice:        aws.String("0.0400"),
			Timestamp:        aws.Time(now.Add(-30 * time.Minute)),
		},
		{
			AvailabilityZone: aws.String("eu-west-1a"),
			InstanceType:     ec2types.InstanceType("m5.large"),
			SpotPrice:        aws.String("0.0310"),
			Timestamp:        aws.Time(now),
		},
		{
			AvailabilityZone: aws.String("eu-west-1b"),
			InstanceType:     ec2types.InstanceType("m5.large"),
			SpotPrice:        aws.String("0.0450"),
			Timestamp:        aws.Time(now),
		},
	}}
	p := testProvider(t, pricingClient, spotClient)

	zoneA := viewerpricing.NodeSpec{InstanceType: "m5.large", Zone: "eu-west-1a", CapacityType: "spot", OS: "linux"}
	zoneB := viewerpricing.NodeSpec{InstanceType: "m5.large", Zone: "eu-west-1b", CapacityType: "spot", OS: "linux"}
	p.HourlyPrice(zoneA)
	p.HourlyPrice(zoneB)
	p.refresh(context.Background())

	if price, ok := p.HourlyPrice(zoneA); !ok || price != 0.031 {
		t.Fatalf("zone a price = %v, %v; want 0.031 (newest sample), true", price, ok)
	}
	if price, ok := p.HourlyPrice(zoneB); !ok || price != 0.045 {
		t.Fatalf("zone b price = %v, %v; want 0.045, true", price, ok)
	}
	if spotClient.calls != 1 {
		t.Fatalf("DescribeSpotPriceHistory calls = %d, want 1 (batched per region)", spotClient.calls)
	}
	if len(spotClient.regions) != 1 || spotClient.regions[0] != "eu-west-1" {
		t.Fatalf("spot client regions = %v, want [eu-west-1]", spotClient.regions)
	}
}

func TestProviderFallsBackToOnDemandWhenSpotUnknown(t *testing.T) {
	pricingClient := &fakePricing{docs: []string{priceListDoc("0.1070")}}
	spotClient := &fakeSpot{err: errors.New("api error UnauthorizedOperation: not authorized to perform ec2:DescribeSpotPriceHistory")}
	p := testProvider(t, pricingClient, spotClient)

	spec := viewerpricing.NodeSpec{InstanceType: "m5.large", Zone: "eu-west-1a", CapacityType: "spot", OS: "linux"}
	p.HourlyPrice(spec)
	p.refresh(context.Background())

	price, ok := p.HourlyPrice(spec)
	if !ok || price != 0.107 {
		t.Fatalf("HourlyPrice() = %v, %v; want the on-demand ceiling 0.107, true", price, ok)
	}
	// Something is priced, so the tile shows a number rather than a reason.
	if got := p.Unavailable(); got != "" {
		t.Fatalf("Unavailable() = %q, want empty while a price is known", got)
	}
}

func TestProviderReportsMissingCredentials(t *testing.T) {
	p := testProvider(t, nil, nil)
	spec := viewerpricing.NodeSpec{InstanceType: "m5.large", Zone: "eu-west-1a", OS: "linux"}

	p.HourlyPrice(spec)
	p.refresh(context.Background())

	if _, ok := p.HourlyPrice(spec); ok {
		t.Fatal("HourlyPrice() = ok without credentials")
	}
	if got := p.Unavailable(); got != "no AWS credentials" {
		t.Fatalf("Unavailable() = %q, want \"no AWS credentials\"", got)
	}
}

func TestProviderIgnoresUnpriceableSpecs(t *testing.T) {
	pricingClient := &fakePricing{docs: []string{priceListDoc("0.1070")}}
	p := testProvider(t, pricingClient, nil)

	if _, ok := p.HourlyPrice(viewerpricing.NodeSpec{Zone: "eu-west-1a"}); ok {
		t.Fatal("HourlyPrice() = ok for a node with no instance type")
	}
	p.refresh(context.Background())
	if pricingClient.calls != 0 {
		t.Fatalf("GetProducts calls = %d, want 0 for unpriceable nodes", pricingClient.calls)
	}
}

func TestLowestOnDemandPriceSkipsZeroDimensions(t *testing.T) {
	price, err := lowestOnDemandPrice([]string{priceListDoc("0.0000"), priceListDoc("0.2000"), priceListDoc("0.1070")})
	if err != nil {
		t.Fatal(err)
	}
	if price != 0.107 {
		t.Fatalf("lowestOnDemandPrice() = %v, want 0.107", price)
	}

	if price, err := lowestOnDemandPrice(nil); err != nil || price != 0 {
		t.Fatalf("lowestOnDemandPrice(nil) = %v, %v; want 0, nil", price, err)
	}
	if _, err := lowestOnDemandPrice([]string{"{not json"}); err == nil {
		t.Fatal("lowestOnDemandPrice() error = nil for malformed JSON")
	}
}

func TestShortReason(t *testing.T) {
	tests := map[string]string{
		"no AWS credentials: profile missing":                      "no AWS credentials",
		"api error AccessDeniedException: pricing:GetProducts":     "pricing access denied",
		"dial tcp 10.0.0.1:443: i/o timeout":                       "pricing API unreachable",
		"operation error Pricing: GetProducts, https response err": "operation error Pricing: GetProducts, h…",
	}
	for message, want := range tests {
		if got := shortReason(errors.New(message)); got != want {
			t.Errorf("shortReason(%q) = %q, want %q", message, got, want)
		}
	}
}

func TestPricingOSMapping(t *testing.T) {
	if got := pricingOS("windows"); got != "Windows" {
		t.Errorf("pricingOS(windows) = %q, want Windows", got)
	}
	if got := pricingOS(""); got != "Linux" {
		t.Errorf("pricingOS(\"\") = %q, want Linux", got)
	}
	if got := spotProductDescription("linux"); got != "Linux/UNIX" {
		t.Errorf("spotProductDescription(linux) = %q, want Linux/UNIX", got)
	}
	if got := spotProductDescription("Windows"); got != "Windows" {
		t.Errorf("spotProductDescription(Windows) = %q, want Windows", got)
	}
}
