package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"strings"
	"time"

	etcdclient "github.com/coreos/etcd/client"
	"github.com/docker/docker/api/types/network"
	dockerclient "github.com/docker/docker/client"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gbytes"
	. "github.com/onsi/gomega/gexec"
	mathutils "github.com/projectcalico/libnetwork-plugin/utils/math"
)

var kapi etcdclient.KeysAPI

func init() {
	// Create a random seed
	rand.Seed(time.Now().UTC().UnixNano())

	cfg := etcdclient.Config{Endpoints: []string{"http://127.0.0.1:2379"}}
	c, _ := etcdclient.New(cfg)
	kapi = etcdclient.NewKeysAPI(c)
}

// Assume that the plugin is running along with a clean etd

var _ = Describe("Libnetwork Tests", func() {
	BeforeEach(func() {
		WipeEtcd()
		CreatePool("192.169.0.0/16")
	})

	// Test the docker network commands - no need to test inspect or ls
	Describe("docker network create", func() {
		// TODO There is no coverage of the following two options. I can't see how to make them get passed to the plugin.
		// --label value
		// --aux-address
		Context("checking failure cases", func() {
			It("needs both network and IPAM drivers to be calico", func() {
				session := DockerSession("docker network create $RANDOM -d calico")
				Eventually(session).Should(Exit(1))
				Eventually(session.Err).Should(Say("Error response from daemon: NetworkDriver.CreateNetwork: Non-Calico IPAM driver is used"))
			})
			PIt("doesn't allow a gateway to be specified", func() { //TODO https://github.com/projectcalico/libnetwork-plugin/issues/98
				session := DockerSession("docker network create $RANDOM -d calico --ipam-driver calico-ipam --subnet=192.169.0.0/16 --gateway 192.169.0.1")
				Eventually(session).Should(Exit(1))
				Eventually(session.Err).Should(Say("XXXX"))
			})
			It("requires the subnet to match the calico pool", func() {
				// I'm trying for a /24 but calico is configured with a /16 so it will fail.
				session := DockerSession("docker network create $RANDOM -d calico --ipam-driver calico-ipam --subnet=192.169.0.0/24")
				Eventually(session).Should(Exit(1))
				Eventually(session.Err).Should(Say("Error response from daemon: IpamDriver.RequestPool: The requested subnet must match the CIDR of a configured Calico IP Pool."))
			})
			PIt("rejects --internal being used", func() { //TODO https://github.com/projectcalico/libnetwork-plugin/issues/99
				session := DockerSession("docker network create $RANDOM --internal -d calico --ipam-driver calico-ipam")
				Eventually(session).Should(Exit(1))
				Eventually(session.Err).Should(Say("XXX"))
			})
			It("rejects --ip-range being used", func() {
				session := DockerSession("docker network create $RANDOM --ip-range 192.169.1.0/24 --subnet=192.169.0.0/16 -d calico --ipam-driver calico-ipam")
				Eventually(session).Should(Exit(1))
				Eventually(session.Err).Should(Say("Error response from daemon: IpamDriver.RequestPool: Calico IPAM does not support sub pool configuration on 'docker create network'. Calico IP Pools should be configured first and IP assignment is from those pre-configured pools."))
			})
			PIt("rejects --ipam-opt being used", func() { //TODO https://github.com/projectcalico/libnetwork-plugin/issues/100
				session := DockerSession("docker network create $RANDOM --ipam-opt REJECT -d calico --ipam-driver calico-ipam")
				Eventually(session).Should(Exit(1))
				Eventually(session.Err).Should(Say("XXX"))
			})
			It("rejects --opt being used", func() {
				session := DockerSession("docker network create $RANDOM --opt REJECT -d calico --ipam-driver calico-ipam")
				Eventually(session).Should(Exit(1))
				Eventually(session.Err).Should(Say("Error response from daemon: NetworkDriver.CreateNetwork: Arbitrary options are not supported"))
			})
		})
		Context("checking success cases", func() {
			It("creates a network", func() {
				session := DockerSession("docker network create success$RANDOM -d calico --ipam-driver calico-ipam")
				Eventually(session).Should(Exit(0))
				// There are no observable side effects. We could verify that nothing changed under /calico in etcd?
				// I would like to verify that the correct pools were returned to Docker but it doesn't let us observe that information - https://github.com/docker/docker/issues/28567
			})
			It("creates a network with a subnet", func() {
				session := DockerSession("docker network create success$RANDOM --subnet 192.169.0.0/16 -d calico --ipam-driver calico-ipam")
				Eventually(session).Should(Exit(0))
			})

			PIt("creates a network with IPv6", func() { //TODO - IPv6 support
			})
			PIt("creates a network with IPv6 from a specific subnet", func() {
			})

			//TODO - allow multiple networks from the same pool
		})
	})
	Describe("docker network rm", func() {
		// No options and no side effects
	})
	Describe("docker network connect", func() {
		// TODO
		// Usage:	docker network connect [OPTIONS] NETWORK CONTAINER
		//
		//Connect a container to a network
		//
		//Options:
		//      --alias value           Add network-scoped alias for the container (default [])
		//      --help                  Print usage
		//      --ip string             IP Address
		//      --ip6 string            IPv6 Address
		//      --link value            Add link to another container (default [])
		//      --link-local-ip value   Add a link-local address for the container (default [])
		//
	})
	Describe("docker network disconnect", func() {
		// TODO - no significant options but we should observe the veth going and the endpoint removed from etcd
	})

	//docker create/run
	// create - doesn't have any network interactions until the container is started
	// run - Run a container, check the following
	//		- etcd contains correct info
	//		- host namespace contains the right veth with the right info
	//    - container - container the right routes and interface
	//	run can have the following variations -
	//    --mac-address
	//    --link-local-ip
	//    --ip and --ip6
	Describe("docker run", func() {
		var name string
		BeforeEach(func() {
			name = fmt.Sprintf("run%d", rand.Uint32())
			DockerString(fmt.Sprintf("docker network create %s -d calico --ipam-driver calico-ipam", name))
		})
		AfterEach(func() {
			DockerString(fmt.Sprintf("docker network rm %s", name))
		})

		It("creates a container on a network  and checks all assertions", func() {
			// Create a container that will just sit in the background
			DockerString(fmt.Sprintf("docker run --net %s -tid --name %s busybox", name, name))

			// Gather information for assertions
			docker_endpoint := GetDockerEndpoint(name, name)
			ip := docker_endpoint.IPAddress
			mac := docker_endpoint.MacAddress
			endpoint_id := docker_endpoint.EndpointID
			interface_name := "cali" + endpoint_id[:mathutils.MinInt(11, len(endpoint_id))]

			// Check that the endpoint is created in etcd
			etcd_endpoint := GetEtcdString(fmt.Sprintf("/calico/v1/host/test/workload/libnetwork/libnetwork/endpoint/%s", endpoint_id))
			Expect(etcd_endpoint).Should(MatchJSON(fmt.Sprintf(
				`{"state":"active","name":"%s","mac":"%s","profile_ids":["%s"],"ipv4_nets":["%s/32"],"ipv6_nets":[]}`,
				interface_name, mac, name, ip)))

			// Check profile
			tags := GetEtcdString(fmt.Sprintf("/calico/v1/policy/profile/%s/tags", name))
			labels := GetEtcdString(fmt.Sprintf("/calico/v1/policy/profile/%s/labels", name))
			rules := GetEtcdString(fmt.Sprintf("/calico/v1/policy/profile/%s/rules", name))
			Expect(tags).Should(MatchJSON(fmt.Sprintf(`["%s"]`, name)))
			Expect(labels).Should(MatchJSON("{}"))
			Expect(rules).Should(MatchJSON(fmt.Sprintf(`{"inbound_rules": [{"action": "allow","src_tag": "%s"}],"outbound_rules":[{"action": "allow"}]}`, name)))

			// Check the interface exists on the Host - it has an autoassigned
			// mac and ip, so don't check anything!
			DockerString(fmt.Sprintf("ip addr show %s", interface_name))

			// Make sure the interface in the container exists and has the  assigned ip and mac
			container_interface_string := DockerString(fmt.Sprintf("docker exec -i %s ip addr", name))
			Expect(container_interface_string).Should(ContainSubstring(ip))
			Expect(container_interface_string).Should(ContainSubstring(mac))

			// Make sure the container has the routes we expect
			routes := DockerString(fmt.Sprintf("docker exec -i %s ip route", name))
			Expect(routes).Should(Equal("default via 169.254.1.1 dev cali0 \n169.254.1.1 dev cali0"))

			// Delete container
			DockerString(fmt.Sprintf("docker rm -f %s", name))
		})
		PIt("creates a container with specific MAC", func() { //https://github.com/projectcalico/libnetwork-plugin/issues/97
			// Create a container that will just sit in the background
			chosen_mac := "11:22:33:44:55:66"
			DockerString(fmt.Sprintf("docker run --mac-address %s --net %s -tid --name %s busybox", chosen_mac, name, name))

			// Gather information for assertions
			docker_endpoint := GetDockerEndpoint(name, name)
			ip := docker_endpoint.IPAddress
			mac := docker_endpoint.MacAddress
			endpoint_id := docker_endpoint.EndpointID
			interface_name := "cali" + endpoint_id[:mathutils.MinInt(11, len(endpoint_id))]

			// Make sure the discovered MAC is what we asked for
			Expect(mac).Should(Equal(chosen_mac))

			// Check that the endpoint is created in etcd
			etcd_endpoint := GetEtcdString(fmt.Sprintf("/calico/v1/host/test/workload/libnetwork/libnetwork/endpoint/%s", endpoint_id))
			Expect(etcd_endpoint).Should(MatchJSON(fmt.Sprintf(
				`{"state":"active","name":"%s","mac":"%s","profile_ids":["%s"],"ipv4_nets":["%s/32"],"ipv6_nets":[]}`,
				interface_name, mac, name, ip)))

			// Check the interface exists on the Host - it has an autoassigned
			// mac and ip, so don't check anything!
			DockerString(fmt.Sprintf("ip addr show %s", interface_name))

			// Make sure the interface in the container exists and has the  assigned ip and mac
			container_interface_string := DockerString(fmt.Sprintf("docker exec -i %s ip addr", name))
			Expect(container_interface_string).Should(ContainSubstring(ip))
			Expect(container_interface_string).Should(ContainSubstring(mac))

			// Make sure the container has the routes we expect
			routes := DockerString(fmt.Sprintf("docker exec -i %s ip route", name))
			Expect(routes).Should(Equal("default via 169.254.1.1 dev cali0 \n169.254.1.1 dev cali0"))

			// Delete container
			DockerString(fmt.Sprintf("docker rm -f %s", name))
		})

		PIt("creates a container with specific link local address", func() { // https://github.com/docker/docker/issues/28606
			// Create a container that will just sit in the background
			DockerString(fmt.Sprintf("docker run --link-local-ip 169.254.0.50 %s --net %s -tid --name %s busybox", name, name))

			// Delete container
			DockerString(fmt.Sprintf("docker rm -f %s", name))
		})

		// TODO Ensure that  a specific IP isn't possible without a user specified subnet
		// TODO allocate specific IPs from specific pools - see test cases in https://github.com/projectcalico/libnetwork-plugin/pull/101/files/c8c0386a41a569fbef33fae545ad97fa061470ed#diff-3bca4eb4bf01d8f50e7babc5c90236cc
		// TODO auto alloc IPs from a specific pool - see https://github.com/projectcalico/libnetwork-plugin/pull/101/files/c8c0386a41a569fbef33fae545ad97fa061470ed#diff-2667baf0dbc5ac5027aa29690f306535
		It("creates a container with specific IP", func() {
			// Create a network with a chosen subnet as this is required to choose an IP
			name_subnet := fmt.Sprintf("run%d", rand.Uint32())
			DockerString(fmt.Sprintf("docker network create %s --subnet 192.169.0.0/16 -d calico --ipam-driver calico-ipam", name_subnet))
			// Create a container that will just sit in the background
			chosen_ip := "192.169.50.51"
			DockerString(fmt.Sprintf("docker run --ip %s --net %s -tid --name %s busybox", chosen_ip, name_subnet, name_subnet))

			// Gather information for assertions
			docker_endpoint := GetDockerEndpoint(name_subnet, name_subnet)
			ip := docker_endpoint.IPAddress
			mac := docker_endpoint.MacAddress
			endpoint_id := docker_endpoint.EndpointID
			interface_name_subnet := "cali" + endpoint_id[:mathutils.MinInt(11, len(endpoint_id))]

			Expect(ip).Should(Equal(chosen_ip))

			// Check that the endpoint is created in etcd
			etcd_endpoint := GetEtcdString(fmt.Sprintf("/calico/v1/host/test/workload/libnetwork/libnetwork/endpoint/%s", endpoint_id))
			Expect(etcd_endpoint).Should(MatchJSON(fmt.Sprintf(
				`{"state":"active","name":"%s","mac":"%s","profile_ids":["%s"],"ipv4_nets":["%s/32"],"ipv6_nets":[]}`,
				interface_name_subnet, mac, name_subnet, ip)))

			// Check the interface exists on the Host - it has an autoassigned
			// mac and ip, so don't check anything!
			DockerString(fmt.Sprintf("ip addr show %s", interface_name_subnet))

			// Make sure the interface in the container exists and has the  assigned ip and mac
			container_interface_string := DockerString(fmt.Sprintf("docker exec -i %s ip addr", name_subnet))
			Expect(container_interface_string).Should(ContainSubstring(ip))
			Expect(container_interface_string).Should(ContainSubstring(mac))

			// Make sure the container has the routes we expect
			routes := DockerString(fmt.Sprintf("docker exec -i %s ip route", name_subnet))
			Expect(routes).Should(Equal("default via 169.254.1.1 dev cali0 \n169.254.1.1 dev cali0"))

			// Delete container and network
			DockerString(fmt.Sprintf("docker rm -f %s", name_subnet))
			DockerString(fmt.Sprintf("docker network rm %s", name_subnet))
		})

	})
	//docker stop/rm - stop and rm are the same as far as the plugin is concerned
	// TODO - check that the endpoint is removed from etcd and that the  veth is removed
})

// Get the endpoint information from Docker
func GetDockerEndpoint(container, network string) *network.EndpointSettings {
	os.Setenv("DOCKER_API_VERSION", "1.24")
	os.Setenv("DOCKER_HOST", "http://localhost:5375")
	defer os.Setenv("DOCKER_HOST", "")
	cli, err := dockerclient.NewEnvClient()
	if err != nil {
		panic(err)
	}

	info, err := cli.ContainerInspect(context.Background(), container)
	if err != nil {
		panic(err)
	}

	return info.NetworkSettings.Networks[network]
}

// Get an string for a given etcd path
func GetEtcdString(path string) string {
	// TODO - would be better to use libcalico to get data rather than talking to etcd direct
	resp, err := kapi.Get(context.Background(),
		path, nil)
	if err != nil {
		panic(err)
	}
	return resp.Node.Value
}

// Create a pool in etcd
func CreatePool(pool string) {
	_, err := kapi.Set(context.Background(),
		fmt.Sprintf("/calico/v1/ipam/v4/pool/%s", strings.Replace(pool, "/", "-", -1)),
		fmt.Sprintf(`{"cidr": "%s"}`, pool), nil)
	if err != nil {
		panic(err)
	}
}

// Delete everything under /calico from etcd
func WipeEtcd() {
	_, err := kapi.Delete(context.Background(), "/calico", &etcdclient.DeleteOptions{Dir: true, Recursive: true})
	if err != nil && !etcdclient.IsKeyNotFound(err) {
		panic(err)
	}
}

// Run a command on the  Docker in docker host returning a string
func DockerString(cmd string) string {
	GinkgoWriter.Write([]byte(fmt.Sprintf("Running command [%s]\n", cmd)))
	command := exec.Command("bash", "-c", fmt.Sprintf("docker exec -i dind sh -c '%s'", cmd))
	_, _ = command.StdinPipe()
	out, err := command.Output()
	if err != nil {
		GinkgoWriter.Write(out)
		GinkgoWriter.Write(err.(*exec.ExitError).Stderr)
		Fail("Command failed")
	}
	return strings.TrimSpace(string(out))
}

// Run a docker command command returning the Session
func DockerSession(cmd string) *Session {
	GinkgoWriter.Write([]byte(fmt.Sprintf("Running command [%s]\n", cmd)))
	command := exec.Command("bash", "-c", fmt.Sprintf("docker exec -i dind sh -c '%s'", cmd))
	_, _ = command.StdinPipe()
	session, err := Start(command, GinkgoWriter, GinkgoWriter)
	if err != nil {
		Fail("Command failed")
	}
	return session
}
