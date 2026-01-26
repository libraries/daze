package expvpp

import (
	"encoding/json"
	"expvar"
	"net/http"
	"strconv"
)

// ExpvarAverage is a structure to maintain a running average using expvar.Float.
type ExpvarAverage struct {
	F *expvar.Float
	L float64
}

// Adds a new value to the running average. This is not strictly concurrency-safe, but it won't have much impact on the
// data.
func (e *ExpvarAverage) Add(value float64) {
	e.F.Add((value - e.F.Value()) / e.L)
}

// NewExpvarAverage creates and initializes a new ExpvarAverage instance.
func NewExpvarAverage(name string, length int) *ExpvarAverage {
	return &ExpvarAverage{
		F: expvar.NewFloat(name),
		L: float64(length),
	}
}

// NewExpvarPercent creates a new expvar.Func that calculates the ratio of two expvar.Int or expvar.Float metrics.
func NewExpvarPercent(name string, n string, d string) *expvar.Func {
	f := expvar.Func(func() any {
		v, _ := strconv.ParseFloat(expvar.Get(n).String(), 64)
		w, _ := strconv.ParseFloat(expvar.Get(d).String(), 64)
		return float64(v) / float64(max(1, w))
	})
	expvar.Publish(name, f)
	return &f
}

// ServeMux returns a new http.ServeMux that removes cmdline and memstats from expvar default exports.
// See: https://github.com/golang/go/issues/29105
func ServeMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/vars", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		vars := new(expvar.Map).Init()
		expvar.Do(func(kv expvar.KeyValue) {
			vars.Set(kv.Key, kv.Value)
		})
		vars.Delete("cmdline")
		vars.Delete("memstats")
		msg := map[string]any{}
		err := json.Unmarshal([]byte(vars.String()), &msg)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(err.Error()))
			return
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "    ")
		enc.Encode(msg)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.DefaultServeMux.ServeHTTP(w, r)
	})
	return mux
}
