package kube

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestBuildServiceDNS(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		service   string
		namespace string
		want      string
	}{
		{"80 포트 서비스는 svc.ns 형식의 호스트명을 만든다", "web", "app", "web.app"},
		{"8080 포트 서비스도 svc.ns 형식의 호스트명을 만든다", "api", "app", "api.app"},
		{"대문자가 섞인 입력은 소문자 호스트명으로 바꾼다", "Web-API", "Prod", "web-api.prod"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// given
			service, namespace := tt.service, tt.namespace

			// when
			got := BuildServiceDNS(service, namespace)

			// then
			if got != tt.want {
				t.Fatalf("BuildServiceDNS() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPickHTTPPort(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		ports    []ServicePort
		wantPort int32
	}{
		{"UDP 포트는 건너뛰고 다음 TCP 포트를 고른다", []ServicePort{{Port: 80, Protocol: "UDP"}, {Port: 8080, Protocol: "TCP"}}, 8080},
		{"프로토콜이 비어 있는 포트는 TCP로 취급한다", []ServicePort{{Port: 3000}}, 3000},
		{"지원하는 TCP 포트가 없으면 포트를 고르지 않는다", []ServicePort{{Port: 80, Protocol: "UDP"}, {Port: 443, Protocol: "TCP"}}, 0},
		{"여러 TCP 포트가 있으면 서비스에 나열된 순서대로 첫 지원 포트를 고른다", []ServicePort{{Port: 9000, Protocol: "TCP"}, {Port: 80, Protocol: "TCP"}}, 9000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// given
			svc := Service{Ports: tt.ports}

			// when
			got := PickHTTPPort(&svc)

			// then
			if tt.wantPort == 0 {
				if got != nil {
					t.Fatalf("PickHTTPPort() = %+v, want nil", got)
				}
				return
			}
			if got == nil || got.Port != tt.wantPort {
				t.Fatalf("PickHTTPPort() = %+v, want port %d", got, tt.wantPort)
			}
		})
	}
}

func TestEligibility(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		svc        Service
		wantPort   int32
		wantReason string
	}{
		{"셀렉터와 HTTP 포트가 있으면 고른 포트와 빈 사유를 돌려준다", Service{Selector: selectorApp, Ports: []ServicePort{{Port: 5432, Protocol: "TCP"}, {Port: 8080, Protocol: "TCP"}}}, 8080, ""},
		{"셀렉터가 없으면 no selector 사유를 돌려준다", Service{Ports: []ServicePort{{Port: 80, Protocol: "TCP"}}}, 0, ReasonNoSelector},
		{"셀렉터가 비어 있는 맵이어도 no selector 사유를 돌려준다", Service{Selector: map[string]string{}, Ports: []ServicePort{{Port: 80}}}, 0, ReasonNoSelector},
		{"지원하는 HTTP 포트가 없으면 no HTTP port 사유를 돌려준다", Service{Selector: selectorApp, Ports: []ServicePort{{Port: 80, Protocol: "UDP"}}}, 0, ReasonNoHTTPPort},
		{"셀렉터도 HTTP 포트도 없으면 no selector가 먼저 적용된다", Service{Ports: []ServicePort{{Port: 5432, Protocol: "TCP"}}}, 0, ReasonNoSelector},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// given
			svc := tt.svc

			// when
			port, reason := Eligibility(&svc)

			// then
			if reason != tt.wantReason {
				t.Fatalf("Eligibility() reason = %q, want %q", reason, tt.wantReason)
			}
			if tt.wantReason != "" {
				if port != nil {
					t.Fatalf("Eligibility() port = %+v, want nil", port)
				}
				return
			}
			if port == nil || port.Port != tt.wantPort {
				t.Fatalf("Eligibility() port = %+v, want port %d", port, tt.wantPort)
			}
		})
	}
}

func TestEligibilityError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		reason string
		want   string
	}{
		{"no selector 사유는 셀렉터 없음 오류로 바뀐다", ReasonNoSelector, errServiceHasNoSelector.Error()},
		{"no HTTP port 사유는 지원 포트 없음 오류로 바뀐다", ReasonNoHTTPPort, errNoSupportedHTTPPort.Error()},
		{"알 수 없는 사유는 사유 문장을 그대로 담은 오류가 된다", "other", "other"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// given
			reason := tt.reason

			// when
			err := eligibilityError(reason)

			// then
			if err == nil || err.Error() != tt.want {
				t.Fatalf("eligibilityError(%q) = %v, want %q", reason, err, tt.want)
			}
		})
	}
}

func TestServiceFromCorev1(t *testing.T) {
	t.Parallel()
	newService := func(mutate func(*corev1.Service)) corev1.Service {
		svc := corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"},
			Spec: corev1.ServiceSpec{
				ClusterIP: "10.0.0.1",
				Selector:  map[string]string{"app": "web"},
				Ports:     []corev1.ServicePort{{Name: "http", Port: 80, Protocol: corev1.ProtocolTCP, TargetPort: intstr.FromString("web")}},
			},
		}
		mutate(&svc)
		return svc
	}

	t.Run("이름이 있는 targetPort는 TargetPortName으로 보존한다", func(t *testing.T) {
		t.Parallel()
		// given
		svc := newService(func(*corev1.Service) {})

		// when
		got, ok := serviceFromCorev1(svc)

		// then
		if !ok {
			t.Fatal("serviceFromCorev1() ok = false, want true")
		}
		port := got.Ports[0]
		if port.TargetPortName != "web" || port.TargetPort != 0 {
			t.Fatalf("port = %+v, want TargetPortName web and TargetPort 0", port)
		}
	})

	t.Run("정수 targetPort는 TargetPort로 보존한다", func(t *testing.T) {
		t.Parallel()
		// given
		svc := newService(func(s *corev1.Service) { s.Spec.Ports[0].TargetPort = intstr.FromInt32(8081) })

		// when
		got, ok := serviceFromCorev1(svc)

		// then
		if !ok {
			t.Fatal("serviceFromCorev1() ok = false, want true")
		}
		port := got.Ports[0]
		if port.TargetPort != 8081 || port.TargetPortName != "" {
			t.Fatalf("port = %+v, want TargetPort 8081 and empty name", port)
		}
	})

	t.Run("헤드리스 서비스는 제외한다", func(t *testing.T) {
		t.Parallel()
		// given
		svc := newService(func(s *corev1.Service) { s.Spec.ClusterIP = corev1.ClusterIPNone })

		// when
		_, ok := serviceFromCorev1(svc)

		// then
		if ok {
			t.Fatal("serviceFromCorev1() ok = true, want false")
		}
	})

	t.Run("NodePort 서비스는 제외한다", func(t *testing.T) {
		t.Parallel()
		// given
		svc := newService(func(s *corev1.Service) { s.Spec.Type = corev1.ServiceTypeNodePort })

		// when
		_, ok := serviceFromCorev1(svc)

		// then
		if ok {
			t.Fatal("serviceFromCorev1() ok = true, want false")
		}
	})
}
