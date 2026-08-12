import { useState } from 'react';
import * as Crypto from 'expo-crypto';
import { ActivityIndicator, Pressable, StyleSheet, Text, TextInput, View } from 'react-native';
import { repairCategories, validateRepairIntake, type RepairIntakeDraft } from '../repairIntake';
import type { CreateRepairRequestInput } from '../types';
import { Card, colors } from './ProductUi';

type Props = { onSubmit: (input: CreateRepairRequestInput) => Promise<unknown>; onRefresh: () => Promise<unknown>; onPhaseChange?: () => void };

export function RepairIntakeForm({ onSubmit, onRefresh, onPhaseChange }: Props) {
  const [draft, setDraft] = useState<RepairIntakeDraft>({ category: null, detail: '', publicFundingInvolved: null, requestedAmountKrw: '' });
  const [errors, setErrors] = useState<ReturnType<typeof validateRepairIntake>['errors']>({});
  const [phase, setPhase] = useState<'editing' | 'reviewing' | 'submitting' | 'confirmation_pending'>('editing');
  const [submissionError, setSubmissionError] = useState<string | null>(null);
  const update = <K extends keyof RepairIntakeDraft>(key: K, value: RepairIntakeDraft[K]) => { setDraft((current) => ({ ...current, [key]: value })); setErrors((current) => ({ ...current, [key]: undefined })); setSubmissionError(null); };
  const validation = validateRepairIntake(draft);
  const review = () => {
    const nextDraft = draft.idempotencyKey ? draft : { ...draft, idempotencyKey: newRepairIdempotencyKey() };
    if (!draft.idempotencyKey) setDraft(nextDraft);
    const nextValidation = validateRepairIntake(nextDraft);
    if (!nextValidation.valid) { setErrors(nextValidation.errors); return; }
    setErrors({}); setPhase('reviewing'); onPhaseChange?.();
  };
  const submit = async () => {
    const result = validateRepairIntake(draft);
    if (!result.valid) { setErrors(result.errors); setPhase('editing'); return; }
    setPhase('submitting'); setSubmissionError(null);
    try { await onSubmit(result.input); }
    catch (error) {
      const code = error && typeof error === 'object' && 'code' in error ? String((error as { code: unknown }).code) : '';
      if (code === 'PROJECTION_PENDING') { setPhase('confirmation_pending'); onPhaseChange?.(); }
      else { setSubmissionError(code === 'AUTH_REQUIRED' || code === 'APP_CHECK_REQUIRED' ? '로그인 상태를 다시 확인해 주세요.' : code === 'DEVICE_ASSIGNMENT_NOT_FOUND' || code === 'DEVICE_NOT_FOUND' ? '복지관에 기기 등록 상태를 확인해 주세요.' : '입력한 내용은 그대로 두었어요. 연결을 확인한 뒤 다시 보내 주세요.'); setPhase('reviewing'); onPhaseChange?.(); }
    }
  };
  if (phase === 'confirmation_pending') return <Card style={styles.card}><Text accessibilityRole="header" style={styles.title}>접수 여부를 확인하고 있어요</Text><Text style={styles.intro}>요청은 전달됐을 수 있어요. 새 요청을 보내지 않고 현재 상태만 다시 확인합니다.</Text><Pressable accessibilityRole="button" onPress={() => void onRefresh()} style={styles.submitButton}><Text style={styles.submitText}>접수 상태 다시 확인</Text></Pressable></Card>;
  if (phase === 'reviewing' || phase === 'submitting') return <Card style={styles.card}><Text accessibilityRole="header" style={styles.title}>보내기 전 확인해 주세요</Text><View style={styles.reviewBox}><Text style={styles.reviewLabel}>증상</Text><Text style={styles.reviewValue}>{validation.valid ? validation.input.title : ''}</Text><Text style={styles.reviewLabel}>수리비 지원</Text><Text style={styles.reviewValue}>{draft.publicFundingInvolved ? `신청 · ${Number(draft.requestedAmountKrw).toLocaleString('ko-KR')}원` : '지원 없이 상담'}</Text></View>{submissionError ? <Text accessibilityRole="alert" style={styles.errorBox}>{submissionError}</Text> : null}<View style={styles.actionRow}><Pressable accessibilityRole="button" disabled={phase === 'submitting'} onPress={() => { setPhase('editing'); onPhaseChange?.(); }} style={styles.secondaryButton}><Text style={styles.secondaryText}>수정하기</Text></Pressable><Pressable accessibilityRole="button" accessibilityState={{ busy: phase === 'submitting', disabled: phase === 'submitting' }} disabled={phase === 'submitting'} onPress={() => void submit()} style={styles.submitButton}>{phase === 'submitting' ? <ActivityIndicator color="#FFF" /> : null}<Text style={styles.submitText}>{phase === 'submitting' ? '보내는 중…' : '확인하고 보내기'}</Text></Pressable></View></Card>;
  return <Card style={styles.card}><Text accessibilityRole="header" style={styles.title}>수리 요청 내용</Text><Text style={styles.intro}>전화번호·주소·건강정보는 적지 않아도 돼요.</Text>
    <Text style={styles.label}>어느 부분의 문제인가요? <Text style={styles.required}>필수</Text></Text><View accessibilityRole="radiogroup" style={styles.chips}>{repairCategories.map((category) => <Pressable key={category} accessibilityRole="radio" accessibilityState={{ checked: draft.category === category }} onPress={() => update('category', category)} style={[styles.chip, draft.category === category && styles.chipSelected]}><Text style={[styles.chipText, draft.category === category && styles.chipTextSelected]}>{category}</Text></Pressable>)}</View>{errors?.category ? <Text accessibilityRole="alert" style={styles.fieldError}>{errors.category}</Text> : null}
    <Text style={styles.label}>어떤 문제가 있나요? <Text style={styles.required}>필수</Text></Text><TextInput accessibilityLabel="증상 상세 설명" accessibilityHint="문제가 언제 어떻게 생겼는지 적어 주세요" multiline maxLength={490} onChangeText={(value) => update('detail', value)} placeholder="예: 어제부터 오른쪽 바퀴에서 큰 소리가 나요" placeholderTextColor={colors.faint} style={[styles.input, styles.multiline, errors?.detail && styles.inputError]} textAlignVertical="top" value={draft.detail}/><Text style={styles.helper}>{draft.detail.trim().length}/490자</Text>{errors?.detail ? <Text accessibilityRole="alert" style={styles.fieldError}>{errors.detail}</Text> : null}
    <Text style={styles.label}>수리비 지원을 신청할까요? <Text style={styles.required}>필수</Text></Text><View accessibilityRole="radiogroup" style={styles.fundingGroup}>{([{ value: true, label: '복지관 수리비 지원 신청' }, { value: false, label: '지원 없이 상담' }] as const).map((option) => <Pressable key={String(option.value)} accessibilityRole="radio" accessibilityState={{ checked: draft.publicFundingInvolved === option.value }} onPress={() => update('publicFundingInvolved', option.value)} style={[styles.radio, draft.publicFundingInvolved === option.value && styles.radioSelected]}><Text style={styles.radioText}>{option.label}</Text></Pressable>)}</View>{errors?.publicFundingInvolved ? <Text accessibilityRole="alert" style={styles.fieldError}>{errors.publicFundingInvolved}</Text> : null}
    {draft.publicFundingInvolved ? <View><Text style={styles.label}>예상 수리비 <Text style={styles.required}>필수</Text></Text><TextInput accessibilityLabel="예상 수리비" keyboardType="number-pad" onChangeText={(value) => update('requestedAmountKrw', value.replace(/[^0-9]/g, ''))} placeholder="예: 120000" placeholderTextColor={colors.faint} style={[styles.input, errors?.requestedAmountKrw && styles.inputError]} value={draft.requestedAmountKrw}/><Text style={styles.helper}>금액을 모르면 ‘지원 없이 상담’으로 먼저 접수해 주세요.</Text>{errors?.requestedAmountKrw ? <Text accessibilityRole="alert" style={styles.fieldError}>{errors.requestedAmountKrw}</Text> : null}</View> : null}
    <Pressable accessibilityRole="button" onPress={review} style={styles.submitButton}><Text style={styles.submitText}>입력 내용 확인</Text></Pressable></Card>;
}

const styles = StyleSheet.create({card:{marginBottom:28},title:{color:colors.ink,fontSize:21,fontWeight:'800'},intro:{color:colors.muted,fontSize:14,lineHeight:21,marginTop:7,marginBottom:20},label:{color:colors.ink,fontSize:15,fontWeight:'800',marginTop:18},required:{color:'#9A4D22',fontSize:12},chips:{flexDirection:'row',flexWrap:'wrap',gap:8,marginTop:10},chip:{borderColor:colors.border,borderRadius:20,borderWidth:1,minHeight:44,justifyContent:'center',paddingHorizontal:13},chipSelected:{backgroundColor:'#E6F6F1',borderColor:colors.teal},chipText:{color:colors.muted,fontSize:14,fontWeight:'700'},chipTextSelected:{color:colors.teal},input:{backgroundColor:colors.canvas,borderColor:colors.border,borderRadius:13,borderWidth:1,color:colors.ink,fontSize:16,marginTop:9,minHeight:52,paddingHorizontal:14,paddingVertical:12},multiline:{minHeight:104},inputError:{borderColor:colors.orange},helper:{color:colors.muted,fontSize:12,lineHeight:18,marginTop:5},fieldError:{color:'#9A4D22',fontSize:13,lineHeight:19,marginTop:6},fundingGroup:{gap:8,marginTop:10},radio:{borderColor:colors.border,borderRadius:13,borderWidth:1,justifyContent:'center',minHeight:50,paddingHorizontal:14},radioSelected:{backgroundColor:'#E6F6F1',borderColor:colors.teal},radioText:{color:colors.ink,fontSize:15,fontWeight:'700'},reviewBox:{backgroundColor:colors.canvas,borderRadius:14,gap:5,marginTop:20,padding:16},reviewLabel:{color:colors.muted,fontSize:12,fontWeight:'700',marginTop:8},reviewValue:{color:colors.ink,fontSize:15,lineHeight:22,fontWeight:'700'},errorBox:{backgroundColor:colors.orangeWash,borderRadius:13,color:'#8D4A27',fontSize:13,lineHeight:19,marginTop:16,padding:13},actionRow:{flexDirection:'row',gap:8,marginTop:20},submitButton:{alignItems:'center',backgroundColor:colors.teal,borderRadius:15,flex:1,flexDirection:'row',gap:8,justifyContent:'center',marginTop:22,minHeight:54,paddingHorizontal:15},submitText:{color:'#FFF',fontSize:16,fontWeight:'800'},secondaryButton:{alignItems:'center',borderColor:colors.border,borderRadius:15,borderWidth:1,flex:1,justifyContent:'center',marginTop:22,minHeight:54},secondaryText:{color:colors.ink,fontSize:16,fontWeight:'800'}});

function newRepairIdempotencyKey() {
  return Crypto.randomUUID();
}
