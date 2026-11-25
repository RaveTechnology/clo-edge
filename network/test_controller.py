import copy,unittest
from controller import select_target,cache_allowed,network_observation_needed,read_cache,write_cache,workflow_unchanged
import tempfile,json
from pathlib import Path
class SelectionTests(unittest.TestCase):
 def test_recovery_without_accepted_attach_is_inspection_only(self):
  self.s['phase']='RecoveryRequired';self.s['reason']='IPv4 is not confirmed FREE/DOWN before attach'
  self.assertFalse(select_target(self.c,self.s,self.a,self.v)[2])
  self.s['pending']={'kind':'attach','accepted':True};self.assertTrue(select_target(self.c,self.s,self.a,self.v)[2])
 def test_state_changes_cancel_apply(self):
  other=copy.deepcopy(self.s);self.assertTrue(workflow_unchanged(self.s,other));other['phase']='RecoveryRequired';self.assertFalse(workflow_unchanged(self.s,other))
 def test_corrupt_cache_does_not_disable_fresh_observation(self):
  with tempfile.TemporaryDirectory()as d:
   p=Path(d)/'state.json';p.write_text('{');self.assertEqual(read_cache(p),{});write_cache(p,{'ready':True});self.assertEqual(read_cache(p),{'ready':True})
 def test_no_cloud_poll_before_address_transfer(self):
  for phase in ['Stopping','Detaching','Uninitialized','Suspect']:self.assertFalse(network_observation_needed({'phase':phase}))
  for phase in ['Watching','Attaching','Verifying','RecoveryRequired','Completed']:self.assertTrue(network_observation_needed({'phase':phase}))
 def setUp(self):
  self.c={'controller_machine_id':'fixed','core_config_hash':'hash','project_id':'project','address_id':'vip','public_ip':'192.0.2.20','prefix':24,'gateway':'192.0.2.1','edges':[{'server_id':'a','machine_id':'host-a','private_ip':'10.0.0.10'},{'server_id':'b','machine_id':'host-b','private_ip':'10.0.0.11'}]}
  self.s={'machine_id':'fixed','config_hash':'hash','phase':'Verifying','source_id':'a','target_id':'b','active_id':'a'}
  self.a={'id':'vip','address':'192.0.2.20','type':'FIXED','status':'ACTIVE','attached_to':{'entity':'server','id':'b'},'mask':'255.255.255.0','gateway':'192.0.2.1','mac_addr':'02:11:22:33:44:55'}
  self.v={'a':{'id':'a','project':'project','status':'STOPPED','switch_status':'OFF'},'b':{'id':'b','project':'project','status':'ACTIVE','switch_status':'ON'}}
 def test_cache_only_skips_stable_ready_owner(self):
  self.s.update(phase='Completed',active_id='b');last={'ready':True,'server_id':'b','at':100}
  self.assertTrue(cache_allowed(self.c,self.s,last,120))
  self.s['phase']='Verifying';self.assertFalse(cache_allowed(self.c,self.s,last,120));self.s['phase']='Completed'
  self.assertFalse(cache_allowed(self.c,self.s,last,200));last['server_id']='a';self.assertFalse(cache_allowed(self.c,self.s,last,120))
 def test_known_fence_refusal_does_not_create_api_retry_storm(self):
  self.s.update(phase='Completed',active_id='b');last={'ready':False,'blocked_fence_required':True,'core_phase':'Completed','server_id':'b','at':100}
  self.assertTrue(cache_allowed(self.c,self.s,last,120));self.s['phase']='Verifying';self.assertFalse(cache_allowed(self.c,self.s,last,120));self.s['phase']='Completed';self.assertFalse(cache_allowed(self.c,self.s,last,200))
 def test_only_fenced_transfer_can_change_network(self):
  e,p,allowed=select_target(self.c,self.s,self.a,self.v);self.assertTrue(allowed);self.assertEqual(e['server_id'],'b');self.assertEqual(p['mac'],self.a['mac_addr'])
 def test_unfenced_and_unknown_dispatch_are_inspection_only(self):
  self.v['a'].update(status='ACTIVE',switch_status='ON');self.assertFalse(select_target(self.c,self.s,self.a,self.v)[2]);self.v['a'].update(status='STOPPED',switch_status='OFF');self.s['pending']={'kind':'attach','accepted':False};self.assertFalse(select_target(self.c,self.s,self.a,self.v)[2])
 def test_wrong_workflow_cannot_change_network(self):
  self.s['source_id']='b';self.assertFalse(select_target(self.c,self.s,self.a,self.v)[2])
 def test_scope_changes_are_rejected(self):
  for kind in ['host','hash','owner','project','mask','gateway','address']:
   with self.subTest(kind=kind):
    self.setUp()
    if kind=='host':self.s['machine_id']='other'
    if kind=='hash':self.s['config_hash']='other'
    if kind=='owner':self.a['attached_to']['id']='other'
    if kind=='project':self.v['b']['project']='other'
    if kind=='mask':self.a['mask']='255.255.0.0'
    if kind=='gateway':self.a['gateway']='192.0.2.2'
    if kind=='address':self.a['address']='192.0.2.21'
    with self.assertRaises(ValueError):select_target(self.c,self.s,self.a,self.v)
if __name__=='__main__':unittest.main()
