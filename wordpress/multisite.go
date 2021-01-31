package wordpress

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	v1 "k8s.io/api/core/v1"
	extensionv1beta1 "k8s.io/api/extensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/util/homedir"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// IntOrString integer or string
type IntOrString struct {
	Type   Type   `protobuf:"varint,1,opt,name=type,casttype=Type"`
	IntVal int32  `protobuf:"varint,2,opt,name=intVal"`
	StrVal string `protobuf:"bytes,3,opt,name=strVal"`
}

// Type represents the stored type of IntOrString.
type Type int

// Int - Type
const (
	Int intstr.Type = iota
	String
)

type MultiSite struct {
	NameSpace     string
	PodName       string
	ContainerName string
	Config        *rest.Config
	NickName      string
	Ip            string
	Context       context.Context
}

func (ms *MultiSite) GetDomainName() string {
	return fmt.Sprintf("%s-%s", ms.NameSpace, ms.NickName)
}

type ExeRes struct {
	stdOut   string
	stdError string
	err      error
}

func (ers *ExeRes) IsOk() bool {
	if ers.err != nil {
		return true
	}

	for _, content := range strings.Split(ers.stdOut, "\n") {
		if strings.Contains(content, "exec_code") {
			z := strings.Split(content, "=")
			if z[1] != "0" {
				return false
			}
		}
	}
	return true
}

func ExecInPod(config *rest.Config, namespace, podName, command, containerName string) (string, string, error) {
	k8sCli, err := kubernetes.NewForConfig(config)
	if err != nil {
		return "", "", err
	}
	cmd := []string{
		"/bin/bash",
		"-c",
		command,
	}
	const tty = false
	req := k8sCli.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).SubResource("exec").Param("container", containerName)
	req.VersionedParams(
		&v1.PodExecOptions{
			Command: cmd,
			Stdin:   false,
			Stdout:  true,
			Stderr:  true,
			TTY:     tty,
		},
		scheme.ParameterCodec,
	)

	req.Timeout(150)

	var stdout, stderr bytes.Buffer
	exec, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		return "", "", err
	}
	err = exec.Stream(remotecommand.StreamOptions{
		Stdin:  nil,
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		return "", "", err
	}
	return strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), err
}

func (ms *MultiSite) exec(command string) ExeRes {
	stdOut, stdError, err := ExecInPod(ms.Config, ms.NameSpace, ms.PodName, command, ms.ContainerName)
	return ExeRes{stdOut: stdOut, stdError: stdError, err: err}
}

func (ms *MultiSite) CreateDatabase() ExeRes {
	dbName := ms.NickName
	cmd := "mysql --protocol=socket -uroot -p${MYSQL_ROOT_PASSWORD} -e \"create database if not exists %s\";echo exec_code=$?;"
	cmd += "mysql --protocol=socket -uroot -p${MYSQL_ROOT_PASSWORD} -e \"grant all on %s.* to '${MYSQL_USER}'@'%%'\";echo exec_code=$?;"
	cmd += "mysql --protocol=socket -uroot -p${MYSQL_ROOT_PASSWORD} -e \"flush privileges;\"; echo exec_code=$?;"
	cmd = fmt.Sprintf(cmd, dbName, dbName)
	return ms.exec(cmd)
}

func (ms *MultiSite) CreateSiteDir() ExeRes {
	cmd := fmt.Sprintf("cp -rp /usr/src/wordpress /cloudclusters/wordpress/%s; echo exec_code=$?", ms.NickName)
	return ms.exec(cmd)
}

func (ms *MultiSite) InitSite() ExeRes {
	cmd := "gosu www-data wp --path=/cloudclusters/wordpress/%s/ config create --dbuser=${MYSQL_USER} --dbpass=${MYSQL_PASSWORD} --dbname=%s --dbhost=127.0.0.1 --extra-php < /usr/bin/wphttps.php; echo exec_code=$?;"
	cmd = fmt.Sprintf(cmd, ms.NickName, ms.NickName)
	cmd += fmt.Sprintf("gosu www-data wp core install --url=\"https://%s.$(hostname -f|cut -f 5-6 -d \".\")\" --title=\"${WP_TITLE}\" --admin_user=\"${WP_USER}\" --admin_password=\"${WP_PASSWORD}\" --admin_email=\"${WP_EMAIL}\" --path=/cloudclusters/wordpress/%s/ ; echo exec_code=$?;", ms.GetDomainName(), ms.NickName)
	cmd += fmt.Sprintf("gosu www-data wp theme activate twentyseventeen --path=/cloudclusters/wordpress/%s/; echo exec_code=$?;", ms.NickName)
	cmd += fmt.Sprintf("gosu www-data wp plugin install \"WP Super Cache\" --activate --path=/cloudclusters/wordpress/%s/ ; echo exec_code=$?;", ms.NickName)
	cmd += fmt.Sprintf("gosu www-data wp plugin install \"All-in-One WP Migration\" --activate --path=/cloudclusters/wordpress/%s/ ; echo exec_code=$?", ms.NickName)
	return ms.exec(cmd)
}

func (ms *MultiSite) SetVhost() ExeRes {
	cmd := fmt.Sprintf("cp /config/000-default.conf /cloudclusters/config/apache/%s.conf; echo exec_code=$?;", ms.NickName)
	cmd += fmt.Sprintf("sed -i \"s/default_site/%s/g\"  /cloudclusters/config/apache/%s.conf; echo exec_code=$?", ms.NickName, ms.NickName)
	print(cmd)
	return ms.exec(cmd)
}

func (ms *MultiSite) AddDomain() ExeRes {
	cmd := fmt.Sprintf("sed -i \"/DocumentRoot/i \\        ServerName %s.$(hostname -f|cut -f 5-6 -d \".\")\"  /cloudclusters/config/apache/%s.conf; echo exec_code=$?", ms.GetDomainName(), ms.NickName)
	return ms.exec(cmd)
}

func (ms *MultiSite) GetClientet() (*kubernetes.Clientset, error) {
	clientSet, err := kubernetes.NewForConfig(ms.Config)
	if err != nil {
		return nil, err
	}
	return clientSet, nil
}

func (ms *MultiSite) Ingress(action string) error {
	ingressPaths := []extensionv1beta1.HTTPIngressPath{
		extensionv1beta1.HTTPIngressPath{
			Path: "/",
			Backend: extensionv1beta1.IngressBackend{
				ServiceName: ms.NameSpace + "-cms",
				ServicePort: intstr.IntOrString{
					Type:   Int,
					IntVal: 80,
				},
			},
		},
	}
	ingressSpec := extensionv1beta1.IngressSpec{
		Rules: []extensionv1beta1.IngressRule{
			{
				Host: ms.GetDomainName() + ".tripanels.com",
				IngressRuleValue: extensionv1beta1.IngressRuleValue{
					HTTP: &extensionv1beta1.HTTPIngressRuleValue{
						Paths: ingressPaths,
					},
				},
			},
		},
		TLS: []extensionv1beta1.IngressTLS{
			{
				Hosts: []string{
					ms.GetDomainName() + ".tripanels.com",
				},
				SecretName: "certificates",
			},
		},
	}
	ingress := &extensionv1beta1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ms.GetDomainName(),
			Namespace: ms.NameSpace,
		},
		Spec: ingressSpec,
	}
	clientSet, err := ms.GetClientet()
	if err != nil {
		return err
	}
	if action == "create" {
		_, err = clientSet.ExtensionsV1beta1().Ingresses(ms.NameSpace).Create(ms.Context, ingress, metav1.CreateOptions{})
		return err
	} else {
		return clientSet.ExtensionsV1beta1().Ingresses(ms.NameSpace).Delete(ms.Context, ms.GetDomainName(), metav1.DeleteOptions{})
	}
}
func (ms *MultiSite) RestartApache() ExeRes {
	cmd := "supervisorctl restart apache; echo exec_code=$?"
	return ms.exec(cmd)
}

func (ms *MultiSite) CreateSite() error {
	log.Println("Init site")
	if e := ms.CreateSiteDir(); !e.IsOk() {
		return errors.New(e.stdError)
	}
	log.Println("Create database")
	if e := ms.CreateDatabase(); !e.IsOk() {
		return errors.New(e.stdError)
	}
	log.Println("Init Site")
	if e := ms.InitSite(); !e.IsOk() {
		return errors.New(e.stdError)
	}
	log.Println("Set Vhost")
	if e := ms.SetVhost(); !e.IsOk() {
		return errors.New(e.stdError)
	}

	log.Println("Add domain")
	if e := ms.AddDomain(); !e.IsOk() {
		return errors.New(e.stdError)
	}

	log.Println("Restart apache")

	if e := ms.RestartApache(); !e.IsOk() {
		return errors.New(e.stdError)
	}

	log.Println("Add Ingress")
	if err := ms.Ingress("create"); err != nil {
		return err
	}

	log.Println("Add powerdns")
	if e := ms.AddPowerDns(); e != nil {
		return e
	}
	return nil
}

func (ms *MultiSite) AddPowerDns() error {
	params := `
		{
				"rrsets": [{
				"name": "%s.tripanels.com.",
				"changetype": "replace",
				"type": "A",
				"ttl": "86400",
				"records": [{
					"disabled": false,
					"content": "%s"
				}]
			}]
		}
	`
	jsonStr := fmt.Sprintf(params, ms.GetDomainName(), ms.Ip)
	println(jsonStr)
	url := "https://testpdns-api.cloudclusters.net:8000/api/v1/servers/localhost/zones/tripanels.com"
	req, err := http.NewRequest("PATCH", url, bytes.NewBuffer([]byte(jsonStr)))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", os.Getenv("PDNSKEY"))

	client := &http.Client{}
	resp, err := client.Do(req)

	if err != nil {
		log.Println("add powerdns error")
		return err
	}
	defer resp.Body.Close()
	return nil

}

func (ms *MultiSite) DeleteDatabase() ExeRes {
	cmd := fmt.Sprintf("mysql --protocol=socket -uroot -p${MYSQL_ROOT_PASSWORD} -e \"drop database if exists %s;\";echo exec_code=$?;", ms.NickName)
	cmd += fmt.Sprintf("mysql --protocol=socket -uroot -p${MYSQL_ROOT_PASSWORD} -e \"revoke all on %s.* from '${MYSQL_USER}'@'%%';\"; echo exec_code=$?;", ms.NickName)
	return ms.exec(cmd)
}

func (ms *MultiSite) DeleteSiteFile() ExeRes {
	cmd := fmt.Sprintf("if [ -d \"/cloudclusters/wordpress/%s\" ]; then rm -fr /cloudclusters/wordpress/%s; fi; echo exec_code=$?", ms.NickName, ms.NickName)
	return ms.exec(cmd)
}

func (ms *MultiSite) DeleteDomain() ExeRes {
	cmd := fmt.Sprintf("if [ -f \"/cloudclusters/config/apache/%s.conf\" ]; then sed -i \"/%s/d\"  /cloudclusters/config/apache/%s.conf; fi; echo exec_code=$?", ms.NickName, ms.GetDomainName(), ms.NickName)
	print(cmd)
	return ms.exec(cmd)
}

func (ms *MultiSite) DeleteSiteVhost() ExeRes {
	cmd := fmt.Sprintf("if [ -f \"/cloudclusters/config/apache/%s.conf\" ]; then rm -fr /cloudclusters/config/apache/%s.conf; fi; echo exec_code=$?;", ms.NickName, ms.NickName)
	print(cmd)
	return ms.exec(cmd)
}

func (ms *MultiSite) DeleteSite() error {
	fmt.Println("Delete database")
	if e := ms.DeleteDatabase(); !e.IsOk() {
		return errors.New(e.stdError)
	}
	fmt.Println("Delete site file")
	if e := ms.DeleteSiteFile(); !e.IsOk() {
		return errors.New(e.stdError)
	}
	fmt.Println("Delete  domain")
	if e := ms.DeleteDomain(); !e.IsOk() {
		return errors.New(e.stdError)
	}
	fmt.Println("Delete site vhost")
	if e := ms.DeleteSiteVhost(); !e.IsOk() {
		return errors.New(e.stdError)
	}
	fmt.Println("Delete ingress")
	if e := ms.Ingress("delete"); e != nil {
		return e
	}
	return nil
}

func GetConfig() (*rest.Config, error) {
	var kubeconfig *string
	//获取当前用户home文件夹，并获取kubeconfig配置
	print("hello worldt")
	print(kubeconfig)
	if home := homedir.HomeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else { //如果没有获取到，则需要自行配置kubeconfig
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}
	//把用户传递的命令行参数，解析为响应变量的值
	flag.Parse()
	//加载kubeconfig中的apiserver地址、证书配置等信息
	config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		return nil, err
	}
	return config, nil
}
