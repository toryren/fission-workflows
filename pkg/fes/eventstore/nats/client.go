package nats

import (
	"fmt"

	"strings"

	"github.com/fission/fission-workflows/pkg/fes"
	"github.com/fission/fission-workflows/pkg/util/pubsub"
	"github.com/golang/protobuf/proto"
	"github.com/nats-io/go-nats-streaming"
	"github.com/sirupsen/logrus"
)

type EventStore struct {
	pubsub.Publisher
	conn *WildcardConn
	sub  map[fes.Aggregate]stan.Subscription
}

func NewEventStore(conn *WildcardConn) *EventStore {
	return &EventStore{
		Publisher: pubsub.NewPublisher(),
		conn:      conn,
		sub:       map[fes.Aggregate]stan.Subscription{},
	}
}

// Watch a aggregate type
func (es *EventStore) Watch(aggregate fes.Aggregate) error {

	subject := fmt.Sprintf("%s.>", aggregate.Type)
	sub, err := es.conn.Subscribe(subject, func(msg *stan.Msg) {
		event, err := toEvent(msg)
		if err != nil {
			logrus.Error(err)
		}

		logrus.WithFields(logrus.Fields{
			"aggregate.type": event.Aggregate.Type,
			"aggregate.id":   event.Aggregate.Id,
			"event.type":     event.Type,
			"event.id":       event.Id,
			"nats.subject":   msg.Subject,
		}).Info("Publishing aggregate event to subscribers.")

		err = es.Publisher.Publish(event)
		if err != nil {
			logrus.Error(err)
		}
	}, stan.DeliverAllAvailable())
	if err != nil {
		return err
	}

	logrus.Infof("Eventstore client watches:' %s'", subject)
	es.sub[aggregate] = sub
	return nil
}

func (es *EventStore) Close() error {
	return es.conn.Close()
}

func (es *EventStore) HandleEvent(event *fes.Event) error {
	// TODO make generic / configurable whether to fold event into parent's subject
	subject := toSubject(event.Aggregate)
	if event.Parent != nil {
		subject = toSubject(event.Parent)
	}
	data, err := proto.Marshal(event)
	if err != nil {
		return err
	}

	logrus.WithFields(logrus.Fields{
		"event.id":       event.Id,
		"event.type":     event.Type,
		"aggregate.id":   event.Aggregate.Id,
		"aggregate.type": event.Aggregate.Type,
		"nats.subject":   subject,
	}).Info("EventStore client appending event.")

	return es.conn.Publish(subject, data)
}

func (es *EventStore) Get(aggregate *fes.Aggregate) ([]*fes.Event, error) {
	//logrus.WithField("subject", aggregateType).Debug("GET events from event store")
	subject := toSubject(aggregate)

	msgs, err := es.conn.MsgSeqRange(subject, FIRST_MSG, MOST_RECENT_MSG)
	if err != nil {
		return nil, err
	}
	results := []*fes.Event{}
	for _, msg := range msgs {
		event, err := toEvent(msg)
		if err != nil {
			return nil, err
		}
		results = append(results, event)
	}

	return results, nil
}

func (es *EventStore) List(matcher fes.StringMatcher) ([]fes.Aggregate, error) {
	subjects, err := es.conn.List(matcher)
	if err != nil {
		return nil, err
	}
	results := []fes.Aggregate{}
	for _, subject := range subjects {
		a := toAggregate(subject)
		results = append(results, *a)
	}

	return results, nil
}

func toAggregate(subject string) *fes.Aggregate {
	parts := strings.SplitN(subject, ".", 2)
	if len(parts) < 2 {
		return nil
	}
	return &fes.Aggregate{
		Type: parts[0],
		Id:   parts[1],
	}
}

func toSubject(a *fes.Aggregate) string {
	return fmt.Sprintf("%s.%s", a.Type, a.Id)
}

func toEvent(msg *stan.Msg) (*fes.Event, error) {
	e := &fes.Event{}
	err := proto.Unmarshal(msg.Data, e)
	if err != nil {
		return nil, err
	}

	e.Id = fmt.Sprintf("%d", msg.Sequence)
	return e, nil
}
