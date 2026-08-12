import type { ComponentType } from 'react';

export type VisitDateTimePickerProps = {
  value: Date;
  minimumDate: Date;
  maximumDate: Date;
  onChange: (value: Date) => void;
};

export const VisitDateTimePicker: ComponentType<VisitDateTimePickerProps>;
