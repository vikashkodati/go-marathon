/*
Copyright 2014 Rohith All rights reserved.

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

package marathon

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// Marathon is the interface to the marathon API
type Marathon interface {
	// -- APPLICATIONS ---

	// check it see if a application exists
	HasApplication(name string) (bool, error)
	// get a listing of the application ids
	ListApplications(url.Values) ([]string, error)
	// a list of application versions
	ApplicationVersions(name string) (*ApplicationVersions, error)
	// check a application version exists
	HasApplicationVersion(name, version string) (bool, error)
	// change an application to a different version
	SetApplicationVersion(name string, version *ApplicationVersion) (*DeploymentID, error)
	// check if an application is ok
	ApplicationOK(name string) (bool, error)
	// create an application in marathon
	CreateApplication(application *Application) (*Application, error)
	// delete an application
	DeleteApplication(name string) (*DeploymentID, error)
	// update an application in marathon
	UpdateApplication(application *Application) (*DeploymentID, error)
	// a list of deployments on a application
	ApplicationDeployments(name string) ([]*DeploymentID, error)
	// scale a application
	ScaleApplicationInstances(name string, instances int, force bool) (*DeploymentID, error)
	// restart an application
	RestartApplication(name string, force bool) (*DeploymentID, error)
	// get a list of applications from marathon
	Applications(url.Values) (*Applications, error)
	// get a specific application
	Application(name string) (*Application, error)
	// wait of application
	WaitOnApplication(name string, timeout time.Duration) error

	// -- TASKS ---

	// get a list of tasks for a specific application
	Tasks(application string) (*Tasks, error)
	// get a list of all tasks
	AllTasks(v url.Values) (*Tasks, error)
	// get the endpoints for a service on a application
	TaskEndpoints(name string, port int, healthCheck bool) ([]string, error)
	// kill all the tasks for any application
	KillApplicationTasks(applicationID, hostname string, scale bool) (*Tasks, error)
	// kill a single task
	KillTask(taskID string, scale bool) (*Task, error)
	// kill the given array of tasks
	KillTasks(taskIDs []string, scale bool) error

	// --- GROUPS ---

	// list all the groups in the system
	Groups() (*Groups, error)
	// retrieve a specific group from marathon
	Group(name string) (*Group, error)
	// create a group deployment
	CreateGroup(group *Group) error
	// delete a group
	DeleteGroup(name string) (*DeploymentID, error)
	// update a groups
	UpdateGroup(id string, group *Group) (*DeploymentID, error)
	// check if a group exists
	HasGroup(name string) (bool, error)
	// wait for an group to be deployed
	WaitOnGroup(name string, timeout time.Duration) error

	// --- DEPLOYMENTS ---

	// get a list of the deployments
	Deployments() ([]*Deployment, error)
	// delete a deployment
	DeleteDeployment(id string, force bool) (*DeploymentID, error)
	// check to see if a deployment exists
	HasDeployment(id string) (bool, error)
	// wait of a deployment to finish
	WaitOnDeployment(id string, timeout time.Duration) error

	// --- SUBSCRIPTIONS ---

	// a list of current subscriptions
	Subscriptions() (*Subscriptions, error)
	// add a events listener
	AddEventsListener(channel EventsChannel, filter int) error
	// remove a events listener
	RemoveEventsListener(channel EventsChannel)
	// remove our self from subscriptions
	Unsubscribe(string) error

	// --- MISC ---

	// get the marathon url
	GetMarathonURL() string
	// ping the marathon
	Ping() (bool, error)
	// grab the marathon server info
	Info() (*Info, error)
	// retrieve the leader info
	Leader() (string, error)
	// cause the current leader to abdicate
	AbdicateLeader() (string, error)
}

var (
	// ErrInvalidEndpoint is thrown when the marathon url specified was invalid
	ErrInvalidEndpoint = errors.New("invalid Marathon endpoint specified")
	// ErrInvalidResponse is thrown when marathon responds with invalid or error response
	ErrInvalidResponse = errors.New("invalid response from Marathon")
	// ErrDoesNotExist is thrown when the resource does not exists
	ErrDoesNotExist = errors.New("the resource does not exist")
	// ErrMarathonDown is thrown when all the marathon endpoints are down
	ErrMarathonDown = errors.New("all the Marathon hosts are presently down")
	// ErrInvalidArgument is thrown when invalid argument
	ErrInvalidArgument = errors.New("the argument passed is invalid")
	// ErrTimeoutError is thrown when the operation has timed out
	ErrTimeoutError = errors.New("the operation has timed out")
	// ErrConflict is thrown when the resource is conflicting (ie. app already exists)
	ErrConflict = errors.New("conflicting resource")
)

type marathonClient struct {
	sync.RWMutex
	// the configuration for the client
	config Config
	// the flag used to prevent multiple SSE subscriptions
	subscribedToSSE bool
	// the ip address of the client
	ipAddress string
	// the http server */
	eventsHTTP *http.Server
	// the http client use for making requests
	httpClient *http.Client
	// the marathon cluster
	cluster Cluster
	// a map of service you wish to listen to
	listeners map[EventsChannel]int
}

// NewClient creates a new marathon client
//		config:			the configuration to use
func NewClient(config Config) (Marathon, error) {
	// step: we parse the url and build a cluster
	cluster, err := newCluster(config.URL)
	if err != nil {
		return nil, err
	}

	service := new(marathonClient)
	service.config = config
	service.listeners = make(map[EventsChannel]int, 0)
	service.cluster = cluster
	service.httpClient = &http.Client{
		Timeout: (time.Duration(config.RequestTimeout) * time.Second),
	}

	return service, nil
}

// GetMarathonURL retrieves the marathon url
func (r *marathonClient) GetMarathonURL() string {
	return r.cluster.URL()
}

// Ping pings the current marathon endpoint (note, this is not a ICMP ping, but a rest api call)
func (r *marathonClient) Ping() (bool, error) {
	if err := r.apiGet(marathonAPIPing, nil, nil); err != nil {
		return false, err
	}
	return true, nil
}

// TODO remove post, this is a GET request!
func (r *marathonClient) apiGet(uri string, post, result interface{}) error {
	return r.apiCall("GET", uri, post, result)
}

func (r *marathonClient) apiPut(uri string, post, result interface{}) error {
	return r.apiCall("PUT", uri, post, result)
}

func (r *marathonClient) apiPost(uri string, post, result interface{}) error {
	return r.apiCall("POST", uri, post, result)
}

func (r *marathonClient) apiDelete(uri string, post, result interface{}) error {
	return r.apiCall("DELETE", uri, post, result)
}

func (r *marathonClient) apiCall(method, uri string, body, result interface{}) error {
	// Get a member from the cluster
	marathon, err := r.cluster.GetMember()
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/%s", marathon, uri)

	var jsonBody []byte
	if body != nil {
		jsonBody, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}

	// Make the http request to Marathon
	request, err := http.NewRequest(method, url, bytes.NewReader(jsonBody))
	if err != nil {
		return err
	}

	// Add any basic auth and the content headers
	if r.config.HTTPBasicAuthUser != "" {
		request.SetBasicAuth(r.config.HTTPBasicAuthUser, r.config.HTTPBasicPassword)
	}
	request.Header.Add("Content-Type", "application/json")
	request.Header.Add("Accept", "application/json")

	response, err := r.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	respBody, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return err
	}

	log.Printf("apiCall(): %v %v (body: %s) returned [%v] %s\n", request.Method, request.URL.String(), jsonBody, response.Status, respBody)

	if response.StatusCode >= 200 && response.StatusCode <= 299 {
		if result != nil {
			if err := json.Unmarshal(respBody, result); err != nil {
				log.Printf("apiCall(): failed to unmarshall the response from marathon, error: %s\n", err)
				return ErrInvalidResponse
			}
		}
		return nil

	} else if response.StatusCode == 404 {
		return ErrDoesNotExist

	} else if response.StatusCode == 409 {
		return ErrConflict

	} else if response.StatusCode >= 500 {
		return ErrInvalidResponse
	}

	log.Printf("apiCall(): unknown error: %s", respBody)
	return ErrInvalidResponse
}
