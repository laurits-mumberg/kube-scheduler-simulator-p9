package nodenumber

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"
	"strconv"

	"golang.org/x/xerrors"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	frameworkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"
)

// NodeNumber is an example plugin that favors nodes that have the number suffix which is the same as the number suffix of the pod name.
// But if a reverse option is true, it favors nodes that have the number suffix which **isn't** the same as the number suffix of pod name.
//
// For example:
// With reverse option false, when schedule a pod named Pod1, a Node named Node1 gets a lower score than a node named Node9.
//
// NOTE: this plugin only handle single digit numbers only.
type NodeNumber struct {
	fh framework.Handle
	// if reverse is true, it favors nodes that doesn't have the same number suffix.
	//
	// For example:
	// When schedule a pod named Pod1, a Node named Node1 gets a lower score than a node named Node9.
	reverse bool
}

var (
	_ framework.ScorePlugin    = &NodeNumber{}
	_ framework.PreScorePlugin = &NodeNumber{}
	_ framework.PostBindPlugin = &NodeNumber{}
)

const (
	// Name is the name of the plugin used in the plugin registry and configurations.
	Name             = "NodeNumber"
	preScoreStateKey = "PreScore" + Name
)

// Name returns the name of the plugin. It is used in logs, etc.
func (pl *NodeNumber) Name() string {
	return Name
}

// preScoreState computed at PreScore and used at Score.
type preScoreState struct {
	podSuffixNumber int
}

// Clone implements the mandatory Clone interface. We don't really copy the data since
// there is no need for that.
func (s *preScoreState) Clone() framework.StateData {
	return s
}

func (pl *NodeNumber) PreScore(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodes []*framework.NodeInfo) *framework.Status {
	klog.InfoS("execute PreScore on NodeNumber plugin", "pod", klog.KObj(pod))

	podNameLastChar := pod.Name[len(pod.Name)-1:]
	podnum, err := strconv.Atoi(podNameLastChar)
	if err != nil {
		// return success even if its suffix is non-number.
		return nil
	}

	s := &preScoreState{
		podSuffixNumber: podnum,
	}
	state.Write(preScoreStateKey, s)

	return nil
}

func (pl *NodeNumber) EventsToRegister() []framework.ClusterEvent {
	return []framework.ClusterEvent{
		{Resource: framework.Node, ActionType: framework.Add},
	}
}

var ErrNotExpectedPreScoreState = errors.New("unexpected pre score state")

type NodeRequest struct {
	Node string `json:"node"`
}

func (pl *NodeNumber) PostBind(ctx context.Context, state *framework.CycleState, p *v1.Pod, nodeName string) {
	// Data to send
	data := NodeRequest{Node: nodeName}

	// Convert data to JSON
	jsonData, _ := json.Marshal(data)

	// Make the POST request
	resp, err := http.Post("https://p9-scheduler-plugins.vercel.app/log", "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	defer resp.Body.Close()
	// Remove this
	http.Get("https://eojwg1nx782egtx.m.pipedream.net")
}

// Score invoked at the score extension point.
func (pl *NodeNumber) Score(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeName string) (int64, *framework.Status) {

	apiData, apierr := GetData()

	if apierr != nil {
		klog.InfoS("api fail")
		return 22, nil
	}

	nodeList, _ := pl.fh.SnapshotSharedLister().NodeInfos().List()
	idx := slices.IndexFunc(nodeList, func(n *framework.NodeInfo) bool { return n.Node().Name == nodeName })
	location := nodeList[idx].Node().Labels["location"]

	LocationData := GetLocationData(location, apiData)

	renewDiff := (LocationData.RenewableOutput - LocationData.PrimaryLoad) / LocationData.PrimaryLoad

	renewScore := 100 / (1.0 + math.Pow(math.E, (-0.05*100*renewDiff)))

	return int64(math.Round(renewScore)*0.5 + (math.Round(LocationData.BatteryCharge)-20)*0.5), nil

}

func GetLocationBatteryCharge(loc string, data []LocationData) float64 {
	idx := slices.IndexFunc(data, func(d LocationData) bool { return d.Location == loc })
	return data[idx].BatteryCharge
}

func GetLocationData(loc string, data []LocationData) LocationData {
	idx := slices.IndexFunc(data, func(d LocationData) bool { return d.Location == loc })
	return data[idx]
}

type LocationData struct {
	Time            string  `json:"Time"`
	BatteryCharge   float64 `json:"Battery_charge"`
	RenewableOutput float64 `json:"Renewable_output"`
	PrimaryLoad     float64 `json:"Primary_load"`
	UnmetLoad       float64 `json:"Unmet_load"`
	Location        string  `json:"Location"`
}

func GetData() ([]LocationData, error) {
	resp, err := http.Get("https://p9-scheduler-plugins.vercel.app/data")
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var data []LocationData
	err = json.Unmarshal(body, &data)

	if err != nil {
		return nil, err
	}

	return data, nil
}

// ScoreExtensions of the Score plugin.
func (pl *NodeNumber) ScoreExtensions() framework.ScoreExtensions {
	return nil
}

// New initializes a new plugin and returns it.
func New(ctx context.Context, arg runtime.Object, h framework.Handle) (framework.Plugin, error) {
	typedArg := NodeNumberArgs{Reverse: false}
	if arg != nil {
		err := frameworkruntime.DecodeInto(arg, &typedArg)
		if err != nil {
			return nil, xerrors.Errorf("decode arg into NodeNumberArgs: %w", err)
		}
		klog.Info("NodeNumberArgs is successfully applied")
	}
	return &NodeNumber{fh: h, reverse: typedArg.Reverse}, nil
}

// NodeNumberArgs is arguments for node number plugin.
//
//nolint:revive
type NodeNumberArgs struct {
	metav1.TypeMeta

	Reverse bool `json:"reverse"`
}
