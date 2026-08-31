package playground

import (
	"net/http"

	"github.com/pblumer/atlas/playground"
)

// heatMapResp is how often the run used each part of the diagram.
//
// Elements and flows are lists rather than maps because the zeroes matter: a map
// keyed by id would tempt a client to iterate only what is in it, and the parts
// the data never reached are exactly the ones a coverage view is looking for.
type heatMapResp struct {
	Elements []elementUseResp `json:"elements"`
	Flows    []flowUseResp    `json:"flows"`
	// MaxCount is the busiest count in either list — the scale a client colours
	// against. It is stated here so every client shades the same run the same way.
	MaxCount int64 `json:"maxCount"`
}

type elementUseResp struct {
	Id    string `json:"id"`
	Count int64  `json:"count"`
}

// flowUseResp names a sequence flow by the elements it joins. A compiled flow
// carries no diagram id — the engine never needs one — and the client that draws
// this holds the diagram, so it can find the connection between two elements
// itself.
type flowUseResp struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Count int64  `json:"count"`
}

// HandleHeatMap returns the per-element and per-sequence-flow token counts of the
// run, including the parts of the model it never reached.
func (s *Service) HandleHeatMap(w http.ResponseWriter, r *http.Request) {
	out := heatMapResp{Elements: []elementUseResp{}, Flows: []flowUseResp{}}
	s.run(w, r, func(sb *playground.Sandbox) error {
		h, err := sb.HeatMap()
		if err != nil {
			return err
		}
		out = renderHeatMap(h)
		return nil
	}, &out)
}

func renderHeatMap(h playground.HeatMap) heatMapResp {
	out := heatMapResp{
		Elements: make([]elementUseResp, 0, len(h.Elements)),
		Flows:    make([]flowUseResp, 0, len(h.Flows)),
	}
	for _, e := range h.Elements {
		out.Elements = append(out.Elements, elementUseResp{Id: e.Id, Count: e.Count})
		if e.Count > out.MaxCount {
			out.MaxCount = e.Count
		}
	}
	for _, f := range h.Flows {
		out.Flows = append(out.Flows, flowUseResp{From: f.From, To: f.To, Count: f.Count})
		if f.Count > out.MaxCount {
			out.MaxCount = f.Count
		}
	}
	return out
}
