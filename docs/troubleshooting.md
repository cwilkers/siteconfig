# Troubleshooting Guide

## Paused Annotation in ClusterInstance

The `paused` annotation in a `ClusterInstance` is used to pause reconciliation when an irrecoverable
error occurs that requires user intervention. The annotation is set in the `metadata.Annotations`
field as shown below:

```yaml
apiVersion: siteconfig.open-cluster-management.io/v1alpha1
kind: ClusterInstance
metadata:
  annotations:
    clusterinstance.siteconfig.open-cluster-management.io/paused: ""
```

### When is the Paused Annotation Applied?
When an irrecoverable error occurs, the `ClusterInstance` controller in the SiteConfig Operator
sets the paused annotation. Additionally, it updates `ClusterInstance.Status.Paused` with
information about the error, including the reason for the failure. If more details are needed,
users can check the controller logs.

### How to Identify a Paused ClusterInstance?
The `PAUSED` column in `oc get clusterinstances -A` displays when the paused annotation
is applied:

```sh
$ oc get clusterinstances -A
NAMESPACE   NAME         PAUSED        PROVISIONSTATUS   PROVISIONDETAILS          AGE
my-cluster  my-cluster   5m12s         InProgress        Provisioning cluster      61m27s
```

```sh
$ oc get clusterinstance -n my-cluster my-cluster -ojsonpath='{.status.paused}' | jq
{
  "reason": "deletion timeout exceeded for object (BareMetalHost:my-cluster/node-0): Timed out waiting to delete object (BareMetalHost:my-cluster/node-0)",
  "timeSet": "2025-02-10T17:25:40Z"
}
```

### Resolving a Paused ClusterInstance
To resume reconciliation, users must first resolve the underlying issue. Once addressed,
the paused annotation should be manually removed from the `ClusterInstance`:

```sh
oc annotate clusterinstance -n my-cluster my-cluster clusterinstance.siteconfig.open-cluster-management.io/paused-
```

### Example Scenario
A common case where the paused annotation is applied is when the `ClusterInstance` needs to
delete rendered manifests but encounters a deletion timeout, possibly due to a finalizer
on a rendered manifest. In such cases, manual intervention is required to remove the blocking
finalizer before the paused annotation can be removed.

## Node references unknown group

Validation fails when a node sets `spec.nodes[].group` to a name that is not
defined in `spec.groups[]`. The error is similar to:

```text
invalid group definition(s): node "worker-1" references unknown group "workers"
```

The same error can appear in the `ClusterInstanceValidated` condition with a
`Validation failed:` prefix. Check the group names on the resource:

```sh
oc get clusterinstance -n my-cluster my-cluster \
  -o jsonpath='{range .spec.groups[*]}{.name}{"\n"}{end}'
oc get clusterinstance -n my-cluster my-cluster \
  -o jsonpath='{range .spec.nodes[*]}{.hostName}{" -> "}{.group}{"\n"}{end}'
```

Add the missing group to `spec.groups[]`, or change the node's `group` value to
an existing group. Group names are case-sensitive and must match exactly.

## Group has no member nodes

Validation fails when a group is defined but no node references it:

```text
invalid group definition(s): group "storage" has no member nodes
```

This usually means a group was added before its nodes, or the node `group`
values were renamed. Either set `group: "storage"` on at least one node, or
remove the unused group in the same update. Group templates render once for
every `spec.groups[]` entry regardless of membership, so an empty group would
create group-scoped objects with no backing nodes (for example a `NodePool` with
`spec.replicas: 0`). An empty group is therefore a configuration error rather
than a silently skipped entry.

