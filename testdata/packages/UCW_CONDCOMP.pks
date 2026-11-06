create or replace package ucw_condcomp as
  function feature_name return varchar2;
  function describe_value (p_value in number) return varchar2;
end ucw_condcomp;
/
