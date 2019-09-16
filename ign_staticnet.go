package main // import "github.com/rsevilla87/ign-staticnet"

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path"
	"runtime/pprof"
	"strings"
	"text/template"

	"github.com/vincent-petithory/dataurl"

	ignition "github.com/coreos/ignition/config/v2_2"
	igntypes "github.com/coreos/ignition/config/v2_2/types"

	mux "github.com/gorilla/mux"
)

var templatesDir = "templates"

type Nic struct {
	Name    string
	IP      string
	Mask    string
	Gateway string
	DNS     string
}

type Bond struct {
	Name    string
	IP      string
	Mask    string
	Gateway string
	DNS     string
}

type Slave struct {
	Name string
	Bond string
}

func handleError(err error) {
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func fileFromBytes(path string, username string, mode int, contents []byte) igntypes.File {
	return igntypes.File{
		Node: igntypes.Node{
			Filesystem: "root",
			Path:       path,
			User: &igntypes.NodeUser{
				Name: username,
			},
		},
		FileEmbedded1: igntypes.FileEmbedded1{
			Mode: &mode,
			Contents: igntypes.FileContents{
				Source: dataurl.EncodeBytes(contents),
			},
		},
	}
}

func addTemplate(ignConfig *igntypes.Config, templateFile string, path string, data interface{}) {
	var buf bytes.Buffer
	tmpl, err := template.ParseFiles(templateFile)
	handleError(err)
	err = tmpl.Execute(&buf, data)
	handleError(err)
	ignFile := fileFromBytes(path, "root", 0644, buf.Bytes())
	ignConfig.Storage.Files = append(ignConfig.Storage.Files, ignFile)
}

func nicWrapper(ignConfigs map[string]igntypes.Config) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		var data []byte
		addr := strings.Split(r.RemoteAddr, ":")
		nicFile := path.Join(templatesDir, "nic.tmpl")
		nic := Nic{
			Name:    vars["nic"],
			IP:      addr[0],
			Mask:    vars["mask"],
			Gateway: vars["gateway"],
			DNS:     vars["dns"],
		}
		nicTemplate, err := template.ParseFiles(nicFile)
		handleError(err)
		buf := bytes.NewBuffer(data)
		err = nicTemplate.Execute(buf, nic)
		nicPath := fmt.Sprintf("/etc/sysconfig/network-scripts/ifcfg-%s", vars["nic"])
		f := fileFromBytes(nicPath, "root", 0644, buf.Bytes())
		ignData := ignConfigs[vars["type"]]
		ignData.Storage.Files = append(ignData.Storage.Files, f)
		data, err = json.Marshal(ignData)
		handleError(err)
		w.Write(data)
	}
}

func bondWrapper(ignConfigs map[string]igntypes.Config) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		var data []byte
		addr := strings.Split(r.RemoteAddr, ":")
		slaveFile := path.Join(templatesDir, "bondSlave.tmpl")
		bondFile := path.Join(templatesDir, "bond.tmpl")
		bond := Bond{
			Name:    vars["bond"],
			IP:      addr[0],
			Mask:    vars["mask"],
			Gateway: vars["gateway"],
			DNS:     vars["dns"],
		}
		nic1 := Slave{
			Name: vars["nic1"],
			Bond: vars["bond"],
		}
		nic2 := Slave{
			Name: vars["nic1"],
			Bond: vars["bond"],
		}
		ignData := ignConfigs[vars["type"]]
		bondPath := fmt.Sprintf("/etc/sysconfig/network-scripts/ifcfg-%s", vars["bond"])
		nic1Path := fmt.Sprintf("/etc/sysconfig/network-scripts/ifcfg-%s", vars["nic1"])
		nic2Path := fmt.Sprintf("/etc/sysconfig/network-scripts/ifcfg-%s", vars["nic2"])
		addTemplate(&ignData, bondFile, bondPath, bond)
		addTemplate(&ignData, slaveFile, nic1Path, nic1)
		addTemplate(&ignData, slaveFile, nic2Path, nic2)
		data, err := json.Marshal(ignData)
		handleError(err)
		w.Write(data)
	}
}

func printRoutes(r *mux.Router) []string {
	var routes []string
	fmt.Println("Registered routes")
	r.Walk(func(route *mux.Route, router *mux.Router, ancestors []*mux.Route) error {
		p, err := route.GetPathTemplate()
		handleError(err)
		fmt.Println(p)
		routes = append(routes, p)
		return nil
	})
	return routes
}

func patchIgnition(ign string) ([]byte, error) {
	var patchedIgn []byte
	return patchedIgn, nil
}

func readFromFiles(ignBootstrap string, ignMaster string, ignWorker string) map[string]igntypes.Config {
	ignConfigs := make(map[string]igntypes.Config)
	ignData, err := ioutil.ReadFile(ignBootstrap)
	ignConfig, _, err := ignition.Parse(ignData)
	handleError(err)
	ignConfigs["bootstrap"] = ignConfig
	ignData, err = ioutil.ReadFile(ignMaster)
	ignConfig, _, err = ignition.Parse(ignData)
	handleError(err)
	ignConfigs["master"] = ignConfig
	ignData, err = ioutil.ReadFile(ignWorker)
	ignConfig, _, err = ignition.Parse(ignData)
	handleError(err)
	ignConfigs["worker"] = ignConfig
	return ignConfigs
}

func main() {
	var cpuprofile = flag.String("cpuprofile", "", "write cpu profile to `file`")
	port := flag.Int("port", 8080, "Server port")
	ignBootstrap := flag.String("bootstrap", "bootstrap.ign", "Botstrap Ignition file to patch")
	ignMaster := flag.String("master", "master.ign", "Master Ignition file to patch")
	ignWorker := flag.String("worker", "worker.ign", "Worker Ignition file to patch")
	flag.Parse()
	if *cpuprofile != "" {
		f, err := os.Create(*cpuprofile)
		if err != nil {
			log.Fatal("could not create CPU profile: ", err)
		}
		defer f.Close()
		if err := pprof.StartCPUProfile(f); err != nil {
			log.Fatal("could not start CPU profile: ", err)
		}
	}
	if *ignBootstrap == "" || *ignMaster == "" || *ignWorker == "" {
		flag.PrintDefaults()
		os.Exit(1)
	}
	ignConfigs := readFromFiles(*ignBootstrap, *ignMaster, *ignWorker)
	r := mux.NewRouter()
	nic := r.Path("/{type:bootstrap|master|worker}/nic/{nic}/{mask}/{gateway}/{dns}")
	bond := r.Path("/{type:bootstrap|master|worker}/bond/{bond}/{mask}/{gateway}/{dns}/{nic1}/{nic2}")
	r.HandleFunc("/status", func(w http.ResponseWriter, req *http.Request) {
		for _, route := range printRoutes(r) {
			w.Write([]byte(route + "\n"))
		}
	})
	nic.HandlerFunc(nicWrapper(ignConfigs))
	bond.HandlerFunc(bondWrapper(ignConfigs))
	printRoutes(r)
	fmt.Printf("Listening at %d\n", *port)
	pprof.StopCPUProfile()
	if err := http.ListenAndServe(fmt.Sprintf(":%d", *port), r); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
