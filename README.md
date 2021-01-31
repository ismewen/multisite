### introduction

wordpress多站点管理的一个简单的operator
git

### quick start

- 创建一个站点

`kubectl apply -f config/samples/jcy_v1alpha1_multisite.yaml`

```yaml
apiVersion: jcy.ismewen.com/v1alpha1
kind: MultiSite
metadata:
  name: multisite-sample
  namespace: wordpress-4132
spec:
  # Add fields here
  podname: wordpress-4132-cms-0
  containername: wordpress
  nickname: it1
  ip: 172.107.26.3
  status: Init

```

- 删除站点

删除对应的crd资源文件即可

`kubectl delete  multisites multisite-sample  -n wordpress-4132`
