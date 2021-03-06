// Copyright 2016-2017 VMware, Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package management

import (
	"fmt"
	"path"

	log "github.com/Sirupsen/logrus"

	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/types"
	"github.com/vmware/vic/lib/config"
	"github.com/vmware/vic/lib/install/validate"
	"github.com/vmware/vic/lib/migration"
	"github.com/vmware/vic/pkg/errors"
	"github.com/vmware/vic/pkg/trace"
	"github.com/vmware/vic/pkg/vsphere/compute"
	"github.com/vmware/vic/pkg/vsphere/extraconfig"
	"github.com/vmware/vic/pkg/vsphere/extraconfig/vmomi"
	"github.com/vmware/vic/pkg/vsphere/vm"
)

const (
	vchIDType = "VirtualMachine"
)

func (d *Dispatcher) NewVCHFromID(id string) (*vm.VirtualMachine, error) {
	defer trace.End(trace.Begin(id))

	var err error
	var vmm *vm.VirtualMachine

	moref := &types.ManagedObjectReference{
		Type:  vchIDType,
		Value: id,
	}
	ref, err := d.session.Finder.ObjectReference(d.ctx, *moref)
	if err != nil {
		if !isManagedObjectNotFoundError(err) {
			err = errors.Errorf("Failed to query appliance (%q): %s", moref, err)
			return nil, err
		}
		log.Debugf("Appliance is not found")
		return nil, nil
	}
	ovm, ok := ref.(*object.VirtualMachine)
	if !ok {
		log.Errorf("Failed to find VM %q: %s", moref, err)
		return nil, err
	}
	vmm = vm.NewVirtualMachine(d.ctx, d.session, ovm.Reference())

	// check if it's VCH
	if ok, err = d.isVCH(vmm); err != nil {
		log.Error(err)
		return nil, err
	}
	if !ok {
		err = errors.Errorf("Not a VCH")
		log.Error(err)
		return nil, err
	}
	d.InitDiagnosticLogsFromVCH(vmm)
	return vmm, nil
}

func (d *Dispatcher) NewVCHFromComputePath(computePath string, name string, v *validate.Validator) (*vm.VirtualMachine, error) {
	defer trace.End(trace.Begin(fmt.Sprintf("path %q, name %q", computePath, name)))

	var err error

	parent, err := v.ResourcePoolHelper(d.ctx, computePath)
	if err != nil {
		return nil, err
	}
	d.vchPoolPath = path.Join(parent.InventoryPath, name)
	var vchPool *object.ResourcePool
	if d.isVC {
		vapp, err := d.findVirtualApp(d.vchPoolPath)
		if err != nil {
			log.Errorf("Failed to get VCH virtual app %q: %s", d.vchPoolPath, err)
			return nil, err
		}
		if vapp != nil {
			vchPool = vapp.ResourcePool
		}
	}
	if vchPool == nil {
		vchPool, err = d.session.Finder.ResourcePool(d.ctx, d.vchPoolPath)
		if err != nil {
			log.Errorf("Failed to get VCH resource pool %q: %s", d.vchPoolPath, err)
			return nil, err
		}
	}

	rp := compute.NewResourcePool(d.ctx, d.session, vchPool.Reference())

	if d.session.Cluster, err = rp.GetCluster(d.ctx); err != nil {
		log.Debugf("Unable to get the cluster for the VCH's resource pool: %s", err)
	}

	var vmm *vm.VirtualMachine
	if vmm, err = rp.GetChildVM(d.ctx, d.session, name); err != nil {
		log.Errorf("Failed to get VCH VM: %s", err)
		return nil, err
	}
	if vmm == nil {
		err = errors.Errorf("Didn't find VM %q in resource pool %q", name, rp.Reference())
		log.Error(err)
		return nil, err
	}
	vmm.InventoryPath = path.Join(d.vchPoolPath, name)

	// check if it's VCH
	var ok bool
	if ok, err = d.isVCH(vmm); err != nil {
		log.Error(err)
		return nil, err
	}
	if !ok {
		err = errors.Errorf("Not a VCH")
		log.Error(err)
		return nil, err
	}
	d.InitDiagnosticLogsFromVCH(vmm)
	return vmm, nil
}

func (d *Dispatcher) GetVCHConfig(vm *vm.VirtualMachine) (*config.VirtualContainerHostConfigSpec, error) {
	defer trace.End(trace.Begin(""))

	//this is the appliance vm
	mapConfig, err := vm.FetchExtraConfigBaseOptions(d.ctx)
	if err != nil {
		err = errors.Errorf("Failed to get VM extra config of %q: %s", vm.Reference(), err)
		log.Error(err)
		return nil, err
	}

	data := vmomi.OptionValueSource(mapConfig)
	vchConfig := &config.VirtualContainerHostConfigSpec{}
	result := extraconfig.Decode(data, vchConfig)
	if result == nil {
		err = errors.Errorf("Failed to decode VM configuration %q: %s", vm.Reference(), err)
		log.Error(err)
		return nil, err
	}

	if vchConfig.IsCreating() {
		vmRef := vm.Reference()
		vchConfig.SetMoref(&vmRef)
	}
	return vchConfig, nil
}

// FetchAndMigrateVCHConfig query VCH guestinfo, and try to migrate older version data to latest if the data is old
func (d *Dispatcher) FetchAndMigrateVCHConfig(vm *vm.VirtualMachine) (*config.VirtualContainerHostConfigSpec, error) {
	defer trace.End(trace.Begin(""))

	//this is the appliance vm
	mapConfig, err := vm.FetchExtraConfigBaseOptions(d.ctx)
	if err != nil {
		err = errors.Errorf("Failed to get VM extra config of %q: %s", vm.Reference(), err)
		return nil, err
	}

	kv := vmomi.OptionValueMap(mapConfig)
	newMap, migrated, err := migration.MigrateApplianceConfig(d.ctx, d.session, kv)
	if err != nil {
		err = errors.Errorf("Failed to migrate config of %q: %s", vm.Reference(), err)
		return nil, err
	}
	if !migrated {
		log.Debugf("No need to migrate configuration for %q", vm.Reference())
	}

	data := extraconfig.MapSource(newMap)
	vchConfig := &config.VirtualContainerHostConfigSpec{}
	result := extraconfig.Decode(data, vchConfig)
	if result == nil {
		err = errors.Errorf("Failed to decode migrated VM configuration %q: %s", vm.Reference(), err)
		return nil, err
	}

	return vchConfig, nil
}

func (d *Dispatcher) SearchVCHs(computePath string) ([]*vm.VirtualMachine, error) {
	defer trace.End(trace.Begin(computePath))
	if computePath != "" {
		return d.searchVCHsFromComputePath(computePath)
	}
	if d.session.Datacenter != nil {
		return d.searchVCHsPerDC(d.session.Datacenter)
	}
	dcs, err := d.session.Finder.DatacenterList(d.ctx, "*")
	if err != nil {
		err = errors.Errorf("Failed to get datacenter list: %s", err)
		return nil, err
	}

	var vchs []*vm.VirtualMachine
	for _, dc := range dcs {
		dcVCHs, err := d.searchVCHsPerDC(dc)
		if err != nil {
			return nil, err
		}
		vchs = append(vchs, dcVCHs...)
	}
	return vchs, nil
}

// searchVCHsFromComputePath searches for VCHs in all child ResourcePools under computePath.
// The computePath can itself be a ResourcePool, ComputeResource or ClusterComputeResource.
func (d *Dispatcher) searchVCHsFromComputePath(computePath string) ([]*vm.VirtualMachine, error) {
	defer trace.End(trace.Begin(computePath))

	pools, err := d.session.Finder.ResourcePoolList(d.ctx, path.Join(computePath, "..."))
	if err != nil {
		if _, ok := err.(*find.NotFoundError); ok {
			return nil, nil
		}
	}

	var vchs []*vm.VirtualMachine
	for _, pool := range pools {
		children, err := d.getChildVCHs(pool, true)
		if err != nil {
			return nil, err
		}
		vchs = append(vchs, children...)
	}
	return vchs, nil
}

func (d *Dispatcher) searchVCHsPerDC(dc *object.Datacenter) ([]*vm.VirtualMachine, error) {
	defer trace.End(trace.Begin(dc.InventoryPath))

	var err error
	var pools []*object.ResourcePool

	d.session.Datacenter = dc
	d.session.Finder.SetDatacenter(dc)

	var vchs []*vm.VirtualMachine
	if pools, err = d.session.Finder.ResourcePoolList(d.ctx, "*"); err != nil {
		if _, ok := err.(*find.NotFoundError); ok {
			return vchs, nil
		}
		err = errors.Errorf("Failed to search resource pools for datacenter %q: %s", dc.InventoryPath, err)
		return nil, err
	}

	for _, pool := range pools {
		children, err := d.getChildVCHs(pool, true)
		if err != nil {
			return nil, err
		}
		vchs = append(vchs, children...)
	}
	return vchs, nil
}

// getVCHs will check vm with same name under this resource pool, to see if that's VCH vm, and it will also check children vApp, to see if that's a VCH.
// eventually return all fond VCH VMs
func (d *Dispatcher) getChildVCHs(pool *object.ResourcePool, searchVapp bool) ([]*vm.VirtualMachine, error) {
	defer trace.End(trace.Begin(pool.InventoryPath))

	// check if pool itself contains VCH vm.
	var vchs []*vm.VirtualMachine
	poolName := pool.Name()
	computeResource := compute.NewResourcePool(d.ctx, d.session, pool.Reference())
	vmm, err := computeResource.GetChildVM(d.ctx, d.session, poolName)
	if err != nil {
		return nil, errors.Errorf("Failed to query children VM in resource pool %q: %s", pool.InventoryPath, err)
	}
	if vmm != nil {
		vmm.InventoryPath = path.Join(pool.InventoryPath, poolName)
		if ok, _ := d.isVCH(vmm); ok {
			log.Debugf("%q is VCH", vmm.InventoryPath)
			vchs = append(vchs, vmm)
		}
	}

	if !searchVapp {
		return vchs, nil
	}

	vappPath := path.Join(pool.InventoryPath, "*")
	vapps, err := d.session.Finder.VirtualAppList(d.ctx, vappPath)
	if err != nil {
		if _, ok := err.(*find.NotFoundError); ok {
			return vchs, nil
		}
		log.Debugf("Failed to query vapp %q: %s", vappPath, err)
	}
	for _, vapp := range vapps {
		childVCHs, err := d.getChildVCHs(vapp.ResourcePool, false)
		if err != nil {
			return nil, err
		}
		vchs = append(vchs, childVCHs...)
	}
	return vchs, nil
}
