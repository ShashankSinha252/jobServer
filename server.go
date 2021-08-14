package main

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path"
	"regexp"
	"strconv"
	"sync"
	"text/template"
)

const (
	rootPath   = "/"
	viewPath   = "/view/"
	acceptPath = "/accept/"
	rejectPath = "/reject/"
	exitPath   = "/exit"
)

const (
	viewTemplate = "view.html"
	editTemplate = "edit.html"
)

const (
	contentPath    = "data"
	templatePath   = "tmpl/"
	templateSuffix = ".html"
	contentSuffix  = ".txt"
	contentPrefix  = "comment-"
)

type Page struct {
	Title string
	Body  []byte
	ID    string
}

type syncMap struct {
	sync.RWMutex
	idMap map[int]bool
}

type msg struct {
	id   int
	dest string
}

var dirs = []string{"review", "accept", "reject"}
var updateChan = make(chan msg, 100)

var templates = template.Must(template.ParseFiles(
	templatePath+editTemplate,
	templatePath+viewTemplate,
))
var validPath = regexp.MustCompile("^/(accept|reject|view)/([0-9]+)$")

var exit = make(chan struct{})
var layout []syncMap

func loadPage(id int, pageDir string) (*Page, error) {
	index := getIndex("review")

	layout[index].RLock()
	if !layout[index].idMap[id] {
		return nil, fmt.Errorf("entry not present: %d", id)
	}
	layout[index].RUnlock()

	name := strconv.Itoa(id)
	file := path.Join(contentPath, pageDir, name)
	body, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	return &Page{Title: "Job", Body: body, ID: name}, nil
}

func getJobID(rw http.ResponseWriter, r *http.Request) (string, error) {
	m := validPath.FindStringSubmatch(r.URL.Path)
	if m == nil {
		return "", errors.New("invalid page title")
	}

	return m[2], nil
}

func renderTemplate(rw http.ResponseWriter, tmpl string, p *Page) {
	err := templates.ExecuteTemplate(rw, tmpl, p)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
	}
}

func viewHandler(rw http.ResponseWriter, r *http.Request) {
	title, err := getJobID(rw, r)
	if err != nil {
		fmt.Printf("Load failed: %v\n", err)
		http.NotFound(rw, r)
		return
	}

	id, err := strconv.Atoi(title)
	if err != nil {
		fmt.Printf("Load failed: ID: %s [%v]\n", title, err)
		http.NotFound(rw, r)
		return
	}

	p, err := loadPage(id, "review")
	if err != nil {
		fmt.Printf("Load failed: ID: %d [%v]\n", id, err)
		http.NotFound(rw, r)
		return
	}

	renderTemplate(rw, viewTemplate, p)
}

func rootHandler(rw http.ResponseWriter, r *http.Request) {
	if r.URL.Path != rootPath {
		http.NotFound(rw, r)
		return
	}
	// TODO: Add functionality to list entries available to review
	http.Redirect(rw, r, "/view/FrontPage", http.StatusFound)
}

func acceptHandler(rw http.ResponseWriter, r *http.Request) {
	title, err := getJobID(rw, r)
	if err != nil {
		fmt.Printf("Load failed: %v\n", err)
		http.NotFound(rw, r)
		return
	}

	id, err := strconv.Atoi(title)
	if err != nil {
		fmt.Printf("Load failed: ID: %s [%v]\n", title, err)
		http.NotFound(rw, r)
		return
	}

	updateChan <- msg{id, "accept"}
	random := getRandomId()
	newPath := "/view/" + strconv.Itoa(random)
	http.Redirect(rw, r, newPath, http.StatusFound)
}

func getRandomId() int {
	id := -1

	index := getIndex("review")
	sm := &layout[index]
	sm.RLock()
	for id = range sm.idMap {
		fmt.Printf("Random ID: %d\n", id)
		break
	}
	sm.RUnlock()

	return id
}

func getIndex(path string) int {
	for index, dir := range dirs {
		if path == dir {
			return index
		}
	}
	return -1
}

func update() {
	for {
		// TODO: Add graceful handling
		m := <-updateChan

		index := getIndex("review")
		sm := &layout[index]
		sm.Lock()
		if sm.idMap[m.id] {
			sm.idMap[m.id] = false
		} else {
			continue
		}
		sm.Unlock()

		index = getIndex(m.dest)
		sm = &layout[index]
		sm.Lock()
		if !sm.idMap[m.id] {
			sm.idMap[m.id] = true
		}
		sm.Unlock()

		file := strconv.Itoa(m.id)
		oldPath := path.Join(contentPath, "review", file)
		newPath := path.Join(contentPath, m.dest, file)
		err := os.Rename(oldPath, newPath)
		if err != nil {
			fmt.Printf("Move failed: %s -> %s [%v]\n", oldPath, newPath, err)
		}
	}
}

func rejectHandler(rw http.ResponseWriter, r *http.Request) {
	title, err := getJobID(rw, r)
	if err != nil {
		fmt.Printf("Load failed: %v\n", err)
		http.NotFound(rw, r)
		return
	}

	id, err := strconv.Atoi(title)
	if err != nil {
		fmt.Printf("Load failed: ID: %s [%v]\n", title, err)
		http.NotFound(rw, r)
		return
	}

	updateChan <- msg{id, "reject"}

	random := getRandomId()
	newPath := "/view/" + strconv.Itoa(random)
	http.Redirect(rw, r, newPath, http.StatusFound)
}

func exitHandler(rw http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(rw, "Terminating server...")
	close(exit)
}

func getListOfFiles(path string) []int {
	fileIDs := []int{}

	dir, err := os.Open(path)
	if err != nil {
		fmt.Printf("Error to access %s: %v\n", path, err)
		return fileIDs
	}

	filenames, err := dir.Readdirnames(0)
	if err != nil {
		fmt.Printf("Error to read files: %v\n", err)
		return fileIDs
	}

	for _, name := range filenames {
		id, err := strconv.Atoi(name)
		if err != nil || id == 0 {
			fmt.Printf("Issue with conversion for filename : %s\n", name)
			continue
		}
		fileIDs = append(fileIDs, id)
	}

	return fileIDs
}

func initData() []syncMap {
	smList := []syncMap{}

	for _, dir := range dirs {
		ids := getListOfFiles("data/" + dir)
		m := make(map[int]bool)
		for _, id := range ids {
			m[id] = true
		}
		smList = append(smList, syncMap{idMap: m})
	}

	return smList
}

func main() {

	layout = initData()

	go update()
	http.HandleFunc(rootPath, rootHandler)
	http.HandleFunc(viewPath, viewHandler)
	http.HandleFunc(acceptPath, acceptHandler)
	http.HandleFunc(rejectPath, rejectHandler)
	http.HandleFunc(exitPath, exitHandler)

	go func() {
		log.Fatal(http.ListenAndServe(":8080", nil))
	}()

	<-exit
	// TODO: Need to improve termination logic
	fmt.Println("Initiate graceful termination")
	fmt.Println("Gracefully terminated")

}
