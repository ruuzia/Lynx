package feline

import (
    "encoding/json"
    "log"
    "net/http"
    "os"
    "os/exec"
    "strconv"
    "time"
	"html/template"
)

var lynxSessions = map[UserId]*Session {}

type Session struct {
    username string;
    id UserId;
    location string;
    file string;
    page interface{};
    builderPage BuilderPage;
}
type SessionToken string
type UserId int


type IndexPage struct {
    Name string
}

type BuilderPage struct {
    Title string `json:"title"`
    Text string `json:"text"`
    ReturnTo string
    ErrorMsg string
}

func StartSession(w http.ResponseWriter, r *http.Request, user User) {
    debug.Printf("Starting session %s\n", user.Name)
    Login(w, &user)
    if _, exists := lynxSessions[user.Id]; !exists {
        lynxSessions[user.Id] = &Session{
            username: user.Name,
            id: user.Id,
        }
    }

    http.Redirect(w, r, "/", http.StatusFound)
}

/**********************************
 *** SESSION PAGE DISPATCHERS *****
 **********************************/

func dispatchSettings(w http.ResponseWriter, r *http.Request, session *Session) {
    type ReviewTypeDesc struct {
        Code string
        Title string
        Description string
    }

    type SettingsPage struct {
        Options []ReviewTypeDesc
    }

    session.location = "settings"
    session.page = SettingsPage{
        []ReviewTypeDesc{
            {
                Code: "in_order",
                Title: "In order",
                Description: "Review lines in order",
            },
            {
                Code: "random",
                Title: "Random order",
                Description: "Review lines from cues in a random order",
            },
            {
                Code: "cues",
                Title: "Cues from lines",
                Description: "Advanced: recall cues from lines",
            },
            {
                Code: "no_cues",
                Title: "No cues",
                Description: "Advanced: recall lines only based on order",
            },
        },
    }
    
    sessionUpdatePage(w, r)
}

type FileSelectPage struct {
    Files []string
}

func dispatchFileSelect(w http.ResponseWriter, r *http.Request, session *Session) {
    debug.Println("dispatchFileSelect")

    files, err := getFileList(session)
    if err != nil {
        log.Fatal(err)
    }
    debug.Print(files)

    session.location = "fileselect"
    session.page = FileSelectPage {
        Files: files,
    }
    sessionUpdatePage(w, r)
}

func dispatchLineReviewer(w http.ResponseWriter, r *http.Request, session *Session, reviewMethod string) {
    out, err := runLynxCommand(session.username, "lines", "--file", session.file)
    if err != nil {
        debug.Fatal(err)
    }
    var lines []LineData
    err = json.Unmarshal(out, &lines)
    if err != nil {
        debug.Fatal(err)
    }

    type LineReviewerPage struct {
        Lines []LineData
        ReviewMethod string
    }

    session.location = "linereviewer"
    session.page = LineReviewerPage {
        Lines: lines,
        ReviewMethod: reviewMethod,
    }
    sessionUpdatePage(w, r)

}

type LineData struct {
    Id int `json:"id"`
    Cue string `json:"cue"`
    Line string `json:"line"`
    Starred bool `json:"starred"`
    Notes string `json:"notes"`
};


type SessionFinishedPage struct {
}

func dispatchSessionFinished(w http.ResponseWriter, r *http.Request, session *Session) {
    session.location = "sessionfinished"
    session.page = SessionFinishedPage { }
    sessionUpdatePage(w, r)
}

/******************************
 *******************************/

/******************************
 *** SESSION API HANDLERS *****
 *******************************/

func handleStarLine(w http.ResponseWriter, r *http.Request) {
    session, err := ActiveSession(w, r) 
    if err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }

    type StarLinePayload struct {
        Line int `json:"line"`
        Starred bool `json:"starred"`
    }
    var payload StarLinePayload;
    err = json.NewDecoder(r.Body).Decode(&payload)
    if err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }
    debug.Println("starred: ", payload.Starred)
    debug.Println("line: ", strconv.Itoa(payload.Line))

    out, err := runLynxCommand(session.username, "set-flagged", session.file, strconv.Itoa(payload.Line), strconv.FormatBool(payload.Starred))
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    debug.Println(string(out))
}

func handleListLineSets(w http.ResponseWriter, r *http.Request) {
    session, err := ActiveSession(w, r)
    if err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }
    names, err := GetLineSets(session.id)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    json.NewEncoder(w).Encode(&names)
}

func handleUpdateBuilder(w http.ResponseWriter, r *http.Request) {
    session, err := ActiveSession(w, r)
    if err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }
    err = json.NewDecoder(r.Body).Decode(&session.builderPage)
    if err != nil {
        debug.Println(err.Error())
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }
    debug.Printf("title: %s\n", session.builderPage.Title)

    
    w.WriteHeader(http.StatusOK)
}

func handleFinishBuilder(w http.ResponseWriter, r *http.Request) {
    session, err := ActiveSession(w, r)
    if err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }

    tempFile := session.username + "lineset" + strconv.FormatInt(time.Now().Unix(), 10);
    f, err := os.Create(tempFile)
    if err != nil {
        debug.Println("Error: failed to write to file")
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    f.WriteString(session.builderPage.Text);
    f.Close();
    out, err := runLynxCommand(session.username, "add-set", session.builderPage.Title, "../" + tempFile)
    debug.Printf("add-set: %s\n", out)
    if err != nil {
        message := string(err.(*exec.ExitError).Stderr)
        debug.Println(err.Error(), message)
        session.builderPage.ErrorMsg = "Error in format. " + message
        http.Redirect(w, r, "/builder", http.StatusFound)
        return
    }

    AddLineSet(session.id, session.builderPage.Title)

    err = os.Remove(tempFile)
    if err != nil {
        debug.Println(err)
    }

    if session.builderPage.ReturnTo == "/session" && session.location == "fileselect" {
        session.file = session.builderPage.Title
        dispatchSettings(w, r, session)
    }

    http.Redirect(w, r, session.builderPage.ReturnTo, http.StatusFound)
}

func handleLineNotes(w http.ResponseWriter, r *http.Request) {
    session, err := ActiveSession(w, r) 
    if err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }

    type LineNotesPayload struct {
        Line int `json:"line"`
        Notes string `json:"text"`
    }
    var payload LineNotesPayload;
    err = json.NewDecoder(r.Body).Decode(&payload)
    if err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }
    debug.Println("line: ", payload.Line)
    debug.Println("notes: ", payload.Notes)
    runLynxCommand(session.username, "set-notes", session.file, strconv.Itoa(payload.Line), payload.Notes)
}

func handleStartSession(w http.ResponseWriter, r *http.Request) {
    userId, err := CheckAuth(w, r)
    if err != nil {
        redirectLogin(w, r)
        return
    }
    dispatchFileSelect(w, r, lynxSessions[userId])
}

func handleSettings(w http.ResponseWriter, r *http.Request) {
    session, err := ActiveSession(w, r)
    if err != nil {
        redirectLogin(w, r)
        return
    }
    if session.location != "settings" {
        sendPage(w, session)
        return
    }

    r.ParseForm()
    debug.Println(r.Form)
    reviewType := r.Form.Get("reviewtype")
    if reviewType == "" {
        http.Error(w, "Expected reviewtype", http.StatusInternalServerError)
        return
    }

    dispatchLineReviewer(w, r, session, reviewType)
}

func handleFileSelect(w http.ResponseWriter, r *http.Request) {
    debug.Println("handleFileSelect")
    session, err := ActiveSession(w, r)
    if err != nil {
        redirectLogin(w, r)
        return
    }
    if session.location != "fileselect" {
        sendPage(w, session)
        return
    }

    r.ParseForm();
    file := r.Form.Get("file")
    if file == "" {
        http.Error(w, "Missing file", http.StatusBadRequest)
        return
    }
    index, err := strconv.Atoi(file);
    if err != nil {
        http.Error(w, "Invalid index", http.StatusBadRequest)
        return
    }

    data := session.page.(FileSelectPage)

    session.file = data.Files[index]
    dispatchSettings(w, r, session)
}

/******************************
******************************/

func serveHome(w http.ResponseWriter, r *http.Request) {
    session, err := ActiveSession(w, r)
    if err != nil {
        redirectLogin(w, r)
        return
    }

    type HomePage struct {
        ActiveSession bool
        Name string
    }
    data := HomePage {
        ActiveSession: session.location == "linereviewer",
        Name: session.username,
    }

    t, err := template.ParseFiles("./web/templates/index.html")
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    t.Execute(w, data)
}

func serveBuilder(w http.ResponseWriter, r *http.Request) {
    session, err := ActiveSession(w, r)
    if err != nil {
        redirectLogin(w, r)
        return
    }

    r.ParseForm()
    if r.Form.Get("returnTo") != "" {
        session.builderPage.ReturnTo = "/" + r.Form.Get("returnTo")
    }

    t, err := template.ParseFiles("./web/templates/builder.html")
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    debug.Println(session.builderPage)
    t.Execute(w, &session.builderPage)
}

func sessionUpdatePage(w http.ResponseWriter, r *http.Request) {
    // We redirect because the requests made through html forms
    // require it
    http.Redirect(w, r, "/session", http.StatusFound)
}


// Serves the current page in our session
// or redirects to login if not authenticated
func sessionPage(w http.ResponseWriter, r *http.Request) {
    session, err := ActiveSession(w, r)
    if err != nil {
        redirectLogin(w, r)
        return
    }
    
    sendPage(w, session)
    return
}

func sendPage(w http.ResponseWriter, session *Session) {
    t, err := template.ParseFiles("web/templates/" + session.location + ".html")
    if err != nil {
        debug.Println("Error parsing template file")
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    t.Execute(w, session.page)
}
