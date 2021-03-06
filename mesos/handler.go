package mesos

import (
	"strings"
	"sync"
	"time"

	"github.com/Dataman-Cloud/swan/mesosproto"
	"github.com/Dataman-Cloud/swan/types"

	log "github.com/Sirupsen/logrus"
)

const (
	defaultOfferTimeout  = 30 * time.Second
	defaultRefuseTimeout = 5 * time.Second
)

var lock sync.Mutex

type eventHandler func(*mesosproto.Event)

func (s *Scheduler) subscribedHandler(event *mesosproto.Event) {
	var (
		ev = event.GetSubscribed()
		id = ev.FrameworkId
		//interval = ev.GetHeartbeatIntervalSeconds()
	)

	log.Printf("Subscription successful with frameworkId %s", id.GetValue())

	s.framework.Id = id

	if err := s.db.UpdateFrameworkId(id.GetValue()); err != nil {
		log.Errorf("update frameworkid got error:%s", err)
	}

	//s.watchConn(interval)

	s.reconcile()
}

func (s *Scheduler) offersHandler(event *mesosproto.Event) {
	var (
		offers = event.Offers.Offers
	)

	log.Debugf("Receiving %d offer(s) from mesos", len(offers))

	for _, offer := range offers {
		agentId := offer.AgentId.GetValue()

		a := s.getAgent(agentId)
		if a == nil {
			a = newAgent(agentId)
			s.addAgent(a)
		}

		s.addOffer(offer)
	}
}

func (s *Scheduler) rescindedHandler(event *mesosproto.Event) {
	var (
		offerId = event.GetRescind().OfferId.GetValue()
	)

	log.Debugln("Receiving rescind msg for offer ", offerId)

	for _, agent := range s.getAgents() {
		if offer := agent.getOffer(offerId); offer != nil {
			s.removeOffer(offer)
			break
		}
	}
}

func (s *Scheduler) updateHandler(event *mesosproto.Event) {
	lock.Lock()
	defer lock.Unlock()

	var (
		status  = event.GetUpdate().GetStatus()
		state   = status.GetState()
		taskId  = status.TaskId.GetValue()
		healthy = status.GetHealthy()
	)

	log.Debugf("Received status update %s for task %s", status.GetState(), taskId)

	if err := s.AckUpdateEvent(status); err != nil {
		log.Errorf("send status update %s for task %s error: %v", status.GetState(), taskId, err)
	}

	if task, ok := s.tasks[taskId]; ok {
		task.SendStatus(status)
	}

	var appId string
	parts := strings.SplitN(taskId, ".", 3)
	if len(parts) >= 3 {
		appId = parts[2]
	}

	task, err := s.db.GetTask(appId, taskId)
	if err != nil {
		log.Errorf("find task from zk got error: %v", err)
		return
	}

	task.Status = state.String()

	ver, err := s.db.GetVersion(appId, task.Version)
	if err != nil {
		log.Errorf("find task version got error: %v. task %s, version %s", err, task.ID, task.Version)
		return
	}

	if ver.HealthCheck != nil {
		task.Healthy = types.TaskUnHealthy
		if healthy {
			task.Healthy = types.TaskHealthy
		}
	}

	if state != mesosproto.TaskState_TASK_RUNNING {
		task.ErrMsg = status.GetReason().String() + ":" + status.GetMessage()
	}

	if err := s.db.UpdateTask(appId, task); err != nil {
		log.Errorf("update task status error: %v", err)
		return
	}

	typ := types.EventTypeTaskUnhealthy
	if healthy {
		typ = types.EventTypeTaskHealthy
	}

	if err := s.eventmgr.broadcast(&types.TaskEvent{
		Type:           typ,
		AppID:          appId,
		AppAlias:       ver.Proxy.Alias,
		TaskID:         taskId,
		IP:             task.IP,
		Port:           task.Port,
		Weight:         task.Weight,
		GatewayEnabled: ver.Proxy.Enabled,
	}); err != nil {
		log.Errorf("broadcast task event got error: %v", err)
	}

	return
}

func (s *Scheduler) heartbeatHandler(event *mesosproto.Event) {
	log.Debug("Receive heartbeat msg from mesos")

	//s.watcher.Reset(s.heartbeatTimeout)
}

func (s *Scheduler) errHandler(event *mesosproto.Event) {
	ev := event.GetError()

	log.Debugf("Receive error msg %s", ev.GetMessage())
	s.connect()
}

func (s *Scheduler) failureHandler(event *mesosproto.Event) {
	var (
		ev      = event.GetFailure()
		agentId = ev.GetAgentId()
		execId  = ev.GetExecutorId()
		status  = ev.GetStatus()
	)

	if execId != nil {
		log.Debugf("Receive failure msg for executor %s terminated with status %d", execId.GetValue(), status)
		return
	}

	log.Debugf("Receive msg for agent %s removed.", agentId.GetValue())

}

func (s *Scheduler) messageHandler(event *mesosproto.Event) {
}
