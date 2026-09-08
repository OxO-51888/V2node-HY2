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
