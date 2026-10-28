# Configuring SiteConfig Operator

This guide provides instructions for configuring the SiteConfig Operator.
The SiteConfig Operator relies on a ConfigMap to define key parameters that influence its behavior,
such as whether reinstallation operations are allowed and the the maximum number of ClusterInstance resources
that can be reconciled concurrently.

## Configuration overview

The SiteConfig Operator configuration is stored in a ConfigMap named **`siteconfig-operator-configuration`**.
This ConfigMap must reside in the same namespace where the SiteConfig Operator is running.

Key configuration options include:
- **`allowReinstalls`**: Specifies whether reinstallation operations are permitted.
- **`maxConcurrentReconciles`**: Determines the maximum number of concurrent reconcile operations.
**Note**: Changing this value requires restarting the SiteConfig manager pod.

---

## Creating the configuration ConfigMap

If the configuration ConfigMap does not already exist, follow these steps to create it:

1. **Ensure you are targeting the correct namespace:**
   ```sh
   NAMESPACE=<siteconfig-operator-namespace>
   ```

2. **Create the ConfigMap with desired values:**
   ```sh
   cat <<EOF | oc apply -f -
   apiVersion: v1
   kind: ConfigMap
   metadata:
     name: siteconfig-operator-configuration
     namespace: $NAMESPACE
   data:
     allowReinstalls: "false"
     maxConcurrentReconciles: "1"
   EOF
   ```

This will create the ConfigMap with reinstall disabled.

---

## Updating the configuration

To modify the configuration, use the `oc patch` command to update the ConfigMap:

1. **Update the `allowReinstalls` setting:**
   ```sh
   oc patch configmap siteconfig-operator-configuration \
     -n $NAMESPACE \
     --type=json \
     -p '[{"op": "replace", "path": "/data/allowReinstalls", "value": "true"}]'
   ```

2. **Update the `maxConcurrentReconciles` setting:**
   ```sh
   oc patch configmap siteconfig-operator-configuration \
     -n $NAMESPACE \
     --type=json \
     -p '[{"op": "replace", "path": "/data/maxConcurrentReconciles", "value": "10"}]'
   ```

   **Important**: If you modify `maxConcurrentReconciles`, you must restart the SiteConfig manager pod for the
   changes to take effect.

---

## Restarting the SiteConfig manager pod

To apply changes to `maxConcurrentReconciles`, restart the SiteConfig Operator pod:

   ```sh
   oc rollout restart deployment -n $NAMESPACE siteconfig-controller-manager
   ```

   The Operator's deployment will automatically recreate the pod.

---

## Verifying the Configuration

After updating the ConfigMap or restarting the pod:
1. **Check the ConfigMap data:**
   ```sh
   oc get configmap siteconfig-operator-configuration -n $NAMESPACE -o yaml
   ```

2. **Ensure the Operator is using the new configuration:**
   Review the Operator logs to confirm the updated configuration:
   ```sh
   oc logs -n $NAMESPACE --selector app.kubernetes.io/name=siteconfig-controller --follow
   ```

---

## Node grouping

`ClusterInstance` resources can define groups of nodes that share configuration and
render their resources from group templates. This reduces duplication for clusters
where many nodes have the same role and hardware settings.

Grouping is additive: `spec.groups` is optional, and `ClusterInstance` resources
without groups render exactly as they did before grouping was introduced.

### Defining groups and members

Define the groups under `spec.groups[]` and reference a group from a node with
`spec.nodes[].group`:

```yaml
spec:
  groups:
    - name: "workers"
      paused: false
      machineConfigRefs:
        - worker-machine-config
      tuningConfigRefs:
        - worker-tuning-config
      groupTemplateRefs:
        - name: hcp-group-templates-v1
          namespace: siteconfig-operator
      extraLabels:
        NodePool:
          tier: "compute"
      nodeSpec:
        role: "worker"
        bootMode: "UEFI"
        nodeLabels:
          node-role.kubernetes.io/worker: ""
        templateRefs:
          - name: hcp-grouped-node-templates-v1
            namespace: siteconfig-operator
  nodes:
    - hostName: "worker-1.example.com"
      group: "workers"
      bmcAddress: "idrac-virtualmedia+https://192.0.2.1/redfish/v1/Systems/System.Embedded.1"
      bmcCredentialsName:
        name: "worker-1-bmh-secret"
      bootMACAddress: "AA:BB:CC:DD:EE:01"
```

The following rules apply:

- A group `name` must be unique within the `ClusterInstance`, contain at most 63
  characters, and match `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`.
- Every group must have at least one member node, and every `nodes[].group` value
  must reference a defined group.
- `groupTemplateRefs` render once per group, keyed by the group name. A group
  with no member nodes is rejected before rendering.
- Identity fields (`hostName`, `bmcAddress`, `bmcCredentialsName`,
  `bootMACAddress`, `hostRef`, and `nodeNetwork`) belong on each node and cannot
  be set on `groups[].nodeSpec`.

### Merge precedence

For a grouped node, effective configuration is merged from three scopes:

```text
nodes[] > groups[].nodeSpec > .spec
```

Scalars resolve to the most specific value, lists are combined as a set union,
and maps are combined by key with the most specific value winning on a collision.

**Group-only fields** (`groups[]`):

| Field | Notes |
|---|---|
| `name` | Unique; matched by `nodes[].group` |
| `paused` | Exposed to group templates as `.SpecialVars.GroupPaused` |
| `machineConfigRefs` | Exposed to group templates as `.SpecialVars.MachineConfigRefs` |
| `tuningConfigRefs` | Exposed to group templates as `.SpecialVars.TuningConfigRefs` |
| `groupTemplateRefs` | Rendered once per group |
| `extraAnnotations` | Group-rendered objects only; union with `spec.extraAnnotations`, group wins |
| `extraLabels` | Group-rendered objects only; union with `spec.extraLabels`, group wins |

**Shared fields** (`groups[].nodeSpec` and `nodes[]`):

| Field | Merge strategy | Notes |
|---|---|---|
| `rootDeviceHints` | Scalar | |
| `role` | Scalar | |
| `cpuArchitecture` | Scalar | Inherited from `spec` if unset at both scopes |
| `bootMode` | Scalar | |
| `ironicInspect` | Scalar | |
| `automatedCleaningMode` | Scalar | |
| `installerArgs` | Scalar | |
| `ignitionConfigOverride` | Scalar | Node-level BareMetalHost override, distinct from `spec.ignitionConfigOverride` |
| `suppressedManifests` | List union | Applied in the node render loop |
| `pruneManifests` | List union | Same scope rule as `suppressedManifests` |
| `templateRefs` | List union | Union of group and node references |
| `nodeLabels` | Map union | |
| `extraAnnotations` | Map union | Member-node rendered objects only |
| `extraLabels` | Map union | Member-node rendered objects only |

**Node-only fields** (`nodes[]`):

| Field | Notes |
|---|---|
| `name` | |
| `hostName` | |
| `bmcAddress` | |
| `bmcCredentialsName` | Not shareable because some BMC mechanisms delete the Secret with the BareMetalHost |
| `bootMACAddress` | |
| `hostRef` | |
| `nodeNetwork` | |

### Template families

Cluster-level and node-level template references keep their existing meaning.

| Family | Rendered | Examples |
|---|---|---|
| Cluster | Once per `ClusterInstance` | `hcp-cluster-templates-v1`, `ai-cluster-templates-v1`, `ibi-cluster-templates-v1` |
| Node (ungrouped) | Once per ungrouped node | `hcp-node-templates-v1`, `ai-node-templates-v1`, `ibi-node-templates-v1` |

Grouping adds two template families:

| Family | Rendered | Examples |
|---|---|---|
| Group | Once per group | `hcp-group-templates-v1`, `ai-group-templates-v1` |
| Grouped node | Once per grouped node | `hcp-grouped-node-templates-v1`, `ai-grouped-node-templates-v1` |

The operator ships the group and grouped-node families for HyperShift and
Assisted Installer clusters. Image-based installation (IBI) clusters are
single-node and do not ship group templates.

The built-in HCP group templates render one `NodePool` per group with
`spec.replicas` equal to the number of member nodes, plus one `InfraEnv` per
group. The AI group templates render one shared `InfraEnv` per group.

### Migrating an existing ClusterInstance to groups

1. Add `spec.groups[]` entries and set `group` on the nodes that belong to each
   group. Start with groups whose nodes have identical configuration.
2. Move shared values from the nodes into `groups[].nodeSpec`. Leave
   identity fields (`hostName`, `bmcAddress`, `bmcCredentialsName`,
   `bootMACAddress`, `hostRef`, and `nodeNetwork`) on the nodes. Node-level
   values still override group values where both are set.
3. Add `groupTemplateRefs` referencing the cluster type's group templates
   (`hcp-group-templates-v1` or `ai-group-templates-v1`).
4. Add `groups[].nodeSpec.templateRefs` referencing the grouped-node templates
   (`hcp-grouped-node-templates-v1` or `ai-grouped-node-templates-v1`), then
   remove the per-node `*-node-templates-v1` references from grouped nodes.
   If the v1 node templates remain, they are unioned with the grouped-node
   templates and the webhook emits a warning.
5. Apply the updated resource and verify the rendered objects. For HCP clusters,
   each group's `NodePool` should report `spec.replicas` equal to its member
   count.

Groups can be adopted incrementally: grouped and ungrouped nodes may coexist in
the same `ClusterInstance`.

### Group pause

Set `groups[].paused: true` to pause the workloads that the group's templates
manage:

```yaml
groups:
  - name: "workers"
    paused: true
```

SiteConfig passes the value through as `.SpecialVars.GroupPaused` and does not
interpret it for specific resource kinds. The built-in HCP group template maps
it to `spec.pausedUntil: "Fri, 31 Dec 9999 23:59:59 GMT"` on the group's
`NodePool`. Toggling `paused` changes the `ClusterInstance` generation and
re-renders the group; removing `paused` removes `pausedUntil` again. Other
groups and ungrouped nodes are not affected. Because the mapping lives in the
template, custom group templates can use a different pause mechanism.

### Group configuration references

`groups[].machineConfigRefs` and `groups[].tuningConfigRefs` are lists of
ConfigMap names exposed to group templates as `.SpecialVars.MachineConfigRefs`
and `.SpecialVars.TuningConfigRefs`. The built-in HCP group template populates
the `NodePool` `spec.config` and `spec.tuningConfig` fields from these lists.
SiteConfig treats the names as opaque references; the referenced ConfigMaps must
exist in the cluster.

### Defaulting after merge

`role`, `bootMode`, and `automatedCleaningMode` deliberately have no CRD
defaults, because a persisted default on a node would shadow the value from
`groups[].nodeSpec`. Instead, defaults are applied after the merge only when the
field is unset at both the node and group scopes:

| Field | Default |
|---|---|
| `role` | `master` |
| `bootMode` | `UEFI` |
| `automatedCleaningMode` | `disabled` |

---

## Troubleshooting

- If the ConfigMap is missing, the SiteConfig Operator will recreate it with default values.
Check the Operator logs for relevant messages.
- Ensure the ConfigMap name is **`siteconfig-operator-configuration`** and is located in the correct namespace.
