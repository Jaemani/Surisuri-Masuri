import { useState } from 'react';
import * as Crypto from 'expo-crypto';
import { ActivityIndicator, Pressable, ScrollView, StyleSheet, Text, TextInput, View } from 'react-native';

import type { RepairerJobCommand, RepairJob } from '../types';
import { Card, colors, DemoBadge, ProductButton, SectionHeading, StatusPill } from './ProductUi';

type Props = { jobs: RepairJob[]; displayName: string; isDemo: boolean; onSwitchToUser: () => void; onTransition: (command: RepairerJobCommand) => Promise<unknown>; onRefresh: () => Promise<unknown> };
type Panel = 'detail' | 'schedule' | 'submit';

export function RepairerScreen({ jobs, displayName, isDemo, onSwitchToUser, onTransition, onRefresh }: Props) {
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [panel, setPanel] = useState<Panel>('detail');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const selected = jobs.find((job) => job.id === selectedId) ?? null;
  const select = (id: string) => { setSelectedId(id); setPanel('detail'); setError(null); };
  const run = async (command: RepairerJobCommand) => {
    setBusy(true); setError(null);
    try { await onTransition(command); setPanel('detail'); }
    catch (reason) {
      const code = reason && typeof reason === 'object' && 'code' in reason ? String((reason as { code: unknown }).code) : '';
      setError(code === 'REVISION_CONFLICT' ? '다른 담당자가 먼저 변경했어요. 최신 상태를 불러와 주세요.' : code === 'PROJECTION_PENDING' ? '처리는 전달됐을 수 있어요. 새로 보내지 말고 상태를 확인해 주세요.' : '입력 내용은 유지했어요. 연결을 확인한 뒤 다시 시도해 주세요.');
    } finally { setBusy(false); }
  };

  if (selected) return <RepairerJobWorkspace job={selected} panel={panel} busy={busy} error={error} onBack={() => setSelectedId(null)} onPanel={setPanel} onRun={run} onRefresh={onRefresh} />;

  return <View style={styles.screen}><ScrollView contentContainerStyle={styles.content} showsVerticalScrollIndicator={false}>
    <View style={styles.topBar}><View><Text style={styles.kicker}>{displayName}</Text><Text accessibilityRole="header" style={styles.title}>오늘의 작업</Text><Text style={styles.subtitle}>배정된 수리를 한 단계씩 처리하세요.</Text></View>{isDemo ? <Pressable accessibilityRole="button" accessibilityLabel="사용자 화면으로 전환" onPress={onSwitchToUser} style={styles.roleButton}><Text style={styles.roleButtonText}>사용자 화면</Text></Pressable> : null}</View>
    {isDemo ? <DemoBadge label="수리사 역할 · 합성 데모" /> : null}
    <Card style={styles.identityCard}><View style={styles.identityIcon}><Text style={styles.identityGlyph}>✓</Text></View><View style={styles.identityCopy}><Text style={styles.identityTitle}>배정된 작업만 표시돼요</Text><Text style={styles.identityText}>고객 연락처와 지원금 잔액 없이 공개코드로 기기를 대조합니다.</Text></View></Card>
    <SectionHeading title="처리할 작업" action={`${jobs.length}건`} onAction={() => undefined} />
    <View style={styles.jobList}>{jobs.map((job) => <RepairJobCard key={job.id} job={job} onOpen={() => select(job.id)} />)}</View>
    {jobs.length === 0 ? <Card><Text style={styles.emptyTitle}>현재 배정된 작업이 없어요</Text><Text style={styles.emptyText}>복지관에서 새 작업을 배정하면 이곳에 표시됩니다.</Text></Card> : null}
    {isDemo ? <DemoBadge label="표시된 고객·작업 정보는 합성 데이터입니다" /> : null}
  </ScrollView></View>;
}

function RepairJobCard({ job, onOpen }: { job: RepairJob; onOpen: () => void }) {
  const status = statusCopy(job.status);
  return <Card style={styles.jobCard}><View style={styles.jobTop}><StatusPill label={status.label} tone={status.tone} /><Text style={styles.jobDue}>{job.scheduleLabel}</Text></View><Text style={styles.customer}>{job.customerLabel}</Text><Text style={styles.device}>{job.device.model} · {job.device.publicCode}</Text><View style={styles.issueRow}><View style={styles.issueDot} /><Text style={styles.issue}>{job.issue}</Text></View><ProductButton label={primaryLabel(job)} onPress={onOpen} variant="secondary" /></Card>;
}

function RepairerJobWorkspace({ job, panel, busy, error, onBack, onPanel, onRun, onRefresh }: { job: RepairJob; panel: Panel; busy: boolean; error: string | null; onBack: () => void; onPanel: (panel: Panel) => void; onRun: (command: RepairerJobCommand) => Promise<void>; onRefresh: () => Promise<unknown> }) {
  const [scheduleAt, setScheduleAt] = useState('2026-08-20T05:00:00.000Z');
  const [amount, setAmount] = useState('');
  const [categoryCode, setCategoryCode] = useState<'wheel_tire' | 'battery' | 'brakes' | 'controls' | 'seat_frame' | 'other'>('wheel_tire');
  const [actionCode, setActionCode] = useState<'inspect' | 'adjust' | 'repair' | 'replace'>('repair');
  const [key, setKey] = useState<string | null>(null);
  const commandKey = () => key ?? (() => { const next = Crypto.randomUUID(); setKey(next); return next; })();
  const submitAmount = Number(amount);
  const status = statusCopy(job.status);
  const action = job.allowedActions[0];
  return <View style={styles.screen}><ScrollView contentContainerStyle={styles.content} showsVerticalScrollIndicator={false}>
    <Pressable accessibilityRole="button" onPress={onBack} style={styles.backButton}><Text style={styles.backText}>‹ 작업 목록</Text></Pressable>
    <View style={styles.detailHeader}><StatusPill label={status.label} tone={status.tone} /><Text accessibilityRole="header" style={styles.detailTitle}>{job.issue}</Text><Text style={styles.detailMeta}>{job.customerLabel} · {job.device.publicCode}</Text></View>
    <Card style={styles.verifyCard}><Text style={styles.cardKicker}>현장 기기 대조</Text><Text style={styles.verifyCode}>{job.device.publicCode}</Text><Text style={styles.verifyModel}>{job.device.model}</Text><Text style={styles.safeCopy}>기기에 표시된 공개코드와 일치하는지 작업 전에 확인하세요.</Text></Card>
    <View style={styles.stageRow}>{['배정', '일정', '작업', '검증'].map((label, index) => <View key={label} style={styles.stageItem}><View style={[styles.stageDot, index <= stageIndex(job.status) && styles.stageDotActive]}><Text style={[styles.stageNumber, index <= stageIndex(job.status) && styles.stageNumberActive]}>{index + 1}</Text></View><Text style={styles.stageLabel}>{label}</Text></View>)}</View>
    <Card style={styles.actionCard}><Text style={styles.cardKicker}>현재 단계</Text><Text style={styles.actionTitle}>{status.title}</Text><Text style={styles.actionText}>{status.description}</Text><View style={styles.factRow}><Text style={styles.factLabel}>방문 일정</Text><Text style={styles.factValue}>{job.scheduleLabel}</Text></View>{job.billedAmountKrw !== null ? <View style={styles.factRow}><Text style={styles.factLabel}>제출 금액</Text><Text style={styles.factValue}>{job.billedAmountKrw.toLocaleString('ko-KR')}원</Text></View> : null}</Card>
    {error ? <View accessibilityRole="alert" style={styles.errorBox}><Text style={styles.errorText}>{error}</Text><Pressable accessibilityRole="button" onPress={() => void onRefresh()}><Text style={styles.refreshText}>최신 상태 불러오기</Text></Pressable></View> : null}
    {action === 'schedule' && panel === 'detail' ? <ProductButton label="방문 일정 확정" onPress={() => { setKey(null); onPanel('schedule'); }} /> : null}
    {action === 'schedule' && panel === 'schedule' ? <Card style={styles.formCard}><Text accessibilityRole="header" style={styles.formTitle}>방문 일정 확인</Text><Text style={styles.safeCopy}>현재 데모에서는 정해진 예시 일정을 사용합니다. 실제 앱에서는 OS 날짜·시간 선택기를 연결합니다.</Text><View style={styles.reviewBox}><Text style={styles.factLabel}>확정할 일정</Text><Text style={styles.reviewValue}>8월 20일(목) 오후 2:00</Text></View><ProductButton label={busy ? '확정하는 중…' : '이 일정으로 확정'} disabled={busy} onPress={() => void onRun({ action: 'schedule', repairRequestId: job.id, expectedRevision: job.revision, scheduledAt: scheduleAt, idempotencyKey: commandKey() })} /></Card> : null}
    {action === 'start' ? <ProductButton label={busy ? '시작하는 중…' : '현장 확인 후 작업 시작'} disabled={busy} onPress={() => void onRun({ action: 'start', repairRequestId: job.id, expectedRevision: job.revision, idempotencyKey: commandKey() })} /> : null}
    {action === 'resume' ? <ProductButton label={busy ? '불러오는 중…' : '수정 작업 다시 시작'} disabled={busy} onPress={() => void onRun({ action: 'resume', repairRequestId: job.id, expectedRevision: job.revision, idempotencyKey: commandKey() })} /> : null}
    {action === 'submit' && panel === 'detail' ? <ProductButton label="비용 입력 및 제출" onPress={() => { setKey(null); onPanel('submit'); }} /> : null}
    {action === 'submit' && panel === 'submit' ? <Card style={styles.formCard}><Text accessibilityRole="header" style={styles.formTitle}>수리 작업 제출</Text><Text style={styles.safeCopy}>작업 부위와 처리 방법, 청구 금액을 제출합니다. 복지관 확인 전에는 완료로 처리되지 않습니다.</Text><Text style={styles.inputLabel}>작업 부위</Text><View accessibilityRole="radiogroup" style={styles.chips}>{workCategories.map((item) => <Pressable key={item.code} accessibilityRole="radio" accessibilityState={{ checked: categoryCode === item.code }} onPress={() => { setCategoryCode(item.code); setKey(null); }} style={[styles.chip, categoryCode === item.code && styles.chipActive]}><Text style={[styles.chipText, categoryCode === item.code && styles.chipTextActive]}>{item.label}</Text></Pressable>)}</View><Text style={styles.inputLabel}>처리 방법</Text><View accessibilityRole="radiogroup" style={styles.chips}>{workActions.map((item) => <Pressable key={item.code} accessibilityRole="radio" accessibilityState={{ checked: actionCode === item.code }} onPress={() => { setActionCode(item.code); setKey(null); }} style={[styles.chip, actionCode === item.code && styles.chipActive]}><Text style={[styles.chipText, actionCode === item.code && styles.chipTextActive]}>{item.label}</Text></Pressable>)}</View><Text style={styles.inputLabel}>항목 및 총 청구 금액</Text><TextInput accessibilityLabel="수리 청구 금액" keyboardType="number-pad" value={amount} onChangeText={(value) => { setAmount(value.replace(/[^0-9]/g, '')); setKey(null); }} placeholder="예: 85000" placeholderTextColor={colors.faint} style={styles.input}/><ProductButton label={busy ? '제출하는 중…' : '작업 내용 확인하고 제출'} disabled={busy || !Number.isSafeInteger(submitAmount) || submitAmount <= 0} onPress={() => { const category = workCategories.find((item) => item.code === categoryCode)!; const actionItem = workActions.find((item) => item.code === actionCode)!; void onRun({ action: 'submit', repairRequestId: job.id, expectedRevision: job.revision, billedAmountKrw: submitAmount, workItems: [{ categoryCode, categoryLabel: category.label, actionCode, actionLabel: actionItem.label, quantity: 1, lineAmountKrw: submitAmount }], idempotencyKey: commandKey() }); }} /></Card> : null}
    {!action ? <Card style={styles.waitCard}><Text style={styles.waitTitle}>{job.status === 'center_verified' ? '복지관 확인이 끝났어요' : '복지관 검증을 기다리고 있어요'}</Text><Text style={styles.safeCopy}>제출한 기록은 읽기 전용입니다. 지원금 검증과 최종 완료는 복지관에서 처리합니다.</Text></Card> : null}
    {busy ? <ActivityIndicator color={colors.teal} style={styles.loader} /> : null}
  </ScrollView></View>;
}

function statusCopy(status: RepairJob['status']): { label: string; title: string; description: string; tone: 'teal' | 'orange' | 'blue' | 'neutral' } {
  if (status === 'assigned') return { label: '일정 필요', title: '방문 일정을 확정해 주세요', description: '복지관이 배정한 작업입니다. 기기 공개코드를 확인하고 일정을 정하세요.', tone: 'orange' };
  if (status === 'scheduled') return { label: '방문 예정', title: '현장 확인 후 작업을 시작하세요', description: '기기 공개코드가 일치할 때만 작업을 시작합니다.', tone: 'blue' };
  if (status === 'in_progress') return { label: '작업 중', title: '작업을 마치면 결과를 제출하세요', description: '작업 부위·처리 방법·금액을 남기면 복지관 검증이 이어집니다.', tone: 'teal' };
  if (status === 'needs_correction') return { label: '수정 필요', title: '복지관 요청을 확인해 주세요', description: '수정 사유의 구조화된 전달은 다음 계약에서 추가합니다.', tone: 'orange' };
  if (status === 'center_verified') return { label: '기관 확인', title: '복지관 확인이 끝났어요', description: '최종 완료 기록을 기다리는 단계입니다.', tone: 'teal' };
  return { label: '검증 대기', title: '비용을 제출했어요', description: '복지관이 금액과 지원 여부를 확인하고 있습니다.', tone: 'neutral' };
}
function primaryLabel(job: RepairJob) { const action = job.allowedActions[0]; return action === 'schedule' ? '일정 정하기' : action === 'start' ? '작업 열기' : action === 'submit' ? '작업 계속하기' : action === 'resume' ? '수정 요청 확인' : '제출 내용 보기'; }
function stageIndex(status: RepairJob['status']) { return status === 'assigned' ? 0 : status === 'scheduled' ? 1 : status === 'in_progress' || status === 'needs_correction' ? 2 : 3; }
const workCategories = [{ code: 'wheel_tire', label: '바퀴·타이어' }, { code: 'battery', label: '배터리' }, { code: 'brakes', label: '브레이크' }, { code: 'controls', label: '조작부' }, { code: 'seat_frame', label: '시트·프레임' }, { code: 'other', label: '기타' }] as const;
const workActions = [{ code: 'inspect', label: '점검' }, { code: 'adjust', label: '조정' }, { code: 'repair', label: '수리' }, { code: 'replace', label: '교체' }] as const;

const styles = StyleSheet.create({
  screen:{backgroundColor:colors.canvas,flex:1},content:{paddingBottom:38,paddingHorizontal:20,paddingTop:25},topBar:{alignItems:'flex-start',flexDirection:'row',justifyContent:'space-between',marginBottom:14},kicker:{color:colors.orange,fontSize:14,fontWeight:'800',marginBottom:5},title:{color:colors.ink,fontSize:31,fontWeight:'800',letterSpacing:-.5},subtitle:{color:colors.muted,fontSize:14,marginTop:6},roleButton:{backgroundColor:colors.surface,borderColor:colors.border,borderRadius:12,borderWidth:1,marginTop:8,paddingHorizontal:11,paddingVertical:9},roleButtonText:{color:colors.tealDark,fontSize:12,fontWeight:'800'},identityCard:{alignItems:'center',backgroundColor:colors.ink,borderColor:colors.ink,flexDirection:'row',marginBottom:28,marginTop:18,padding:17},identityIcon:{alignItems:'center',backgroundColor:'#3A555A',borderRadius:17,height:50,justifyContent:'center',marginRight:12,width:50},identityGlyph:{color:'#D7ECE5',fontSize:23,fontWeight:'800'},identityCopy:{flex:1},identityTitle:{color:'#FFF',fontSize:16,fontWeight:'800'},identityText:{color:'#B7C8C8',fontSize:12,lineHeight:18,marginTop:4},jobList:{gap:11,marginBottom:20},jobCard:{gap:11,padding:17},jobTop:{alignItems:'center',flexDirection:'row',justifyContent:'space-between'},jobDue:{color:colors.muted,fontSize:12,fontWeight:'700',maxWidth:'55%',textAlign:'right'},customer:{color:colors.ink,fontSize:19,fontWeight:'800'},device:{color:colors.muted,fontSize:13},issueRow:{alignItems:'center',flexDirection:'row'},issueDot:{backgroundColor:colors.orange,borderRadius:4,height:8,marginRight:7,width:8},issue:{color:colors.ink,fontSize:14,fontWeight:'700'},emptyTitle:{color:colors.ink,fontSize:18,fontWeight:'800'},emptyText:{color:colors.muted,fontSize:14,lineHeight:21,marginTop:7},backButton:{alignSelf:'flex-start',minHeight:44,justifyContent:'center'},backText:{color:colors.teal,fontSize:16,fontWeight:'800'},detailHeader:{marginBottom:18,marginTop:8},detailTitle:{color:colors.ink,fontSize:28,fontWeight:'800',letterSpacing:-.4,lineHeight:35,marginTop:12},detailMeta:{color:colors.muted,fontSize:14,marginTop:7},verifyCard:{backgroundColor:colors.ink,borderColor:colors.ink,marginBottom:19},cardKicker:{color:colors.orange,fontSize:12,fontWeight:'800',letterSpacing:.5},verifyCode:{color:'#FFF',fontSize:30,fontWeight:'900',letterSpacing:1,marginTop:10},verifyModel:{color:'#D7E4E2',fontSize:15,fontWeight:'700',marginTop:3},safeCopy:{color:colors.muted,fontSize:14,lineHeight:21,marginTop:10},stageRow:{flexDirection:'row',justifyContent:'space-between',marginBottom:19,paddingHorizontal:6},stageItem:{alignItems:'center',flex:1},stageDot:{alignItems:'center',backgroundColor:'#E7EBE8',borderRadius:18,height:36,justifyContent:'center',width:36},stageDotActive:{backgroundColor:colors.teal},stageNumber:{color:colors.muted,fontSize:14,fontWeight:'800'},stageNumberActive:{color:'#FFF'},stageLabel:{color:colors.muted,fontSize:12,fontWeight:'700',marginTop:6},actionCard:{marginBottom:14},actionTitle:{color:colors.ink,fontSize:20,fontWeight:'800',lineHeight:27,marginTop:7},actionText:{color:colors.muted,fontSize:14,lineHeight:21,marginTop:6},factRow:{borderTopColor:colors.border,borderTopWidth:1,flexDirection:'row',justifyContent:'space-between',marginTop:15,paddingTop:13},factLabel:{color:colors.muted,fontSize:13,fontWeight:'700'},factValue:{color:colors.ink,fontSize:13,fontWeight:'800',maxWidth:'65%',textAlign:'right'},errorBox:{backgroundColor:colors.orangeWash,borderRadius:15,marginBottom:14,padding:14},errorText:{color:'#8D4A27',fontSize:14,lineHeight:20},refreshText:{color:colors.tealDark,fontSize:14,fontWeight:'800',marginTop:9},formCard:{marginTop:2},formTitle:{color:colors.ink,fontSize:21,fontWeight:'800'},reviewBox:{backgroundColor:colors.canvas,borderRadius:13,marginVertical:16,padding:14},reviewValue:{color:colors.ink,fontSize:17,fontWeight:'800',marginTop:5},inputLabel:{color:colors.ink,fontSize:15,fontWeight:'800',marginTop:18},chips:{flexDirection:'row',flexWrap:'wrap',gap:8,marginTop:9},chip:{backgroundColor:colors.canvas,borderColor:colors.border,borderRadius:20,borderWidth:1,minHeight:44,justifyContent:'center',paddingHorizontal:12},chipActive:{backgroundColor:colors.tealWash,borderColor:colors.teal},chipText:{color:colors.muted,fontSize:13,fontWeight:'700'},chipTextActive:{color:colors.tealDark},input:{backgroundColor:colors.canvas,borderColor:colors.border,borderRadius:13,borderWidth:1,color:colors.ink,fontSize:18,marginBottom:16,marginTop:8,minHeight:54,paddingHorizontal:14},waitCard:{backgroundColor:colors.tealWash,borderColor:'#CDE4DD'},waitTitle:{color:colors.tealDark,fontSize:19,fontWeight:'800'},loader:{marginTop:16},
});
