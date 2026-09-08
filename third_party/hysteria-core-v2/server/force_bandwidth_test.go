package server

import "testing"

func TestSelectServerTxForCongestionForcesConfiguredBandwidth(t *testing.T) {
	tests := []struct {
		name      string
		clientRx  uint64
		bandwidth BandwidthConfig
		want      uint64
	}{
		{
			name:     "force configured max tx when client is unknown",
			clientRx: 0,
			bandwidth: BandwidthConfig{
				MaxTx:                500000000 / 8,
				ForceServerBandwidth: true,
			},
			want: 500000000 / 8,
		},
		{
			name:     "force configured max tx when client reports lower bandwidth",
			clientRx: 100000000 / 8,
			bandwidth: BandwidthConfig{
				MaxTx:                500000000 / 8,
				ForceServerBandwidth: true,
			},
			want: 500000000 / 8,
		},
		{
			name:     "keep official clamp without force",
			clientRx: 800000000 / 8,
			bandwidth: BandwidthConfig{
				MaxTx: 500000000 / 8,
			},
			want: 500000000 / 8,
		},
		{
			name:     "keep official fallback without configured max tx",
			clientRx: 100000000 / 8,
			bandwidth: BandwidthConfig{
				ForceServerBandwidth: true,
			},
			want: 100000000 / 8,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := selectServerTxForCongestion(tt.clientRx, tt.bandwidth); got != tt.want {
				t.Fatalf("selectServerTxForCongestion() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestShouldUseBrutalHonorsForcedServerBandwidth(t *testing.T) {
	tests := []struct {
		name                  string
		ignoreClientBandwidth bool
		actualTx              uint64
		bandwidth             BandwidthConfig
		want                  bool
	}{
		{
			name:                  "force brutal even when client bandwidth is ignored",
			ignoreClientBandwidth: true,
			actualTx:              500000000 / 8,
			bandwidth: BandwidthConfig{
				MaxTx:                500000000 / 8,
				ForceServerBandwidth: true,
			},
			want: true,
		},
		{
			name:                  "keep official fallback when forced bandwidth is missing",
			ignoreClientBandwidth: true,
			actualTx:              100000000 / 8,
			bandwidth: BandwidthConfig{
				ForceServerBandwidth: true,
			},
			want: false,
		},
		{
			name:                  "use brutal for normal reported client bandwidth",
			ignoreClientBandwidth: false,
			actualTx:              100000000 / 8,
			bandwidth:             BandwidthConfig{},
			want:                  true,
		},
		{
			name:                  "fall back when no tx is available",
			ignoreClientBandwidth: false,
			actualTx:              0,
			bandwidth: BandwidthConfig{
				MaxTx:                500000000 / 8,
				ForceServerBandwidth: true,
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldUseBrutal(tt.ignoreClientBandwidth, tt.actualTx, tt.bandwidth); got != tt.want {
				t.Fatalf("shouldUseBrutal() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAdvertiseRxAutoUsesFixedServerBandwidth(t *testing.T) {
	tests := []struct {
		name                  string
		ignoreClientBandwidth bool
		bandwidth             BandwidthConfig
		want                  bool
	}{
		{
			name:                  "fixed rx disables auto advertisement",
			ignoreClientBandwidth: true,
			bandwidth: BandwidthConfig{
				MaxRx:                500000000 / 8,
				ForceServerBandwidth: true,
			},
			want: false,
		},
		{
			name:                  "auto remains when no fixed rx is configured",
			ignoreClientBandwidth: true,
			bandwidth: BandwidthConfig{
				ForceServerBandwidth: true,
			},
			want: true,
		},
		{
			name:                  "normal fixed bandwidth remains non-auto",
			ignoreClientBandwidth: false,
			bandwidth: BandwidthConfig{
				MaxRx: 500000000 / 8,
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := advertiseRxAuto(tt.ignoreClientBandwidth, tt.bandwidth); got != tt.want {
				t.Fatalf("advertiseRxAuto() = %v, want %v", got, tt.want)
			}
		})
	}
}
